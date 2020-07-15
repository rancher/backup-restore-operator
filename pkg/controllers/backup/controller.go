package backup

import (
	//"bytes"
	"context"
	"crypto/aes"

	//"crypto/aes"
	//"crypto/cipher"
	//"crypto/rand"
	"encoding/json"
	"fmt"
	//"io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value"
	k8saes "k8s.io/apiserver/pkg/storage/value/encrypt/aes"
	"k8s.io/apiserver/pkg/storage/value/encrypt/envelope"
	"k8s.io/apiserver/pkg/storage/value/encrypt/secretbox"
	"k8s.io/client-go/dynamic"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	//"github.com/kubernetes/kubernetes/pkg/features"
	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	common "github.com/mrajashree/backup/pkg/controllers"
	backupControllers "github.com/mrajashree/backup/pkg/generated/controllers/backupper.cattle.io/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
)

type Handler struct {
	backups         backupControllers.BackupController
	backupTemplates backupControllers.BackupTemplateController
	discoveryClient discovery.DiscoveryInterface
	dynamicClient   dynamic.Interface
}

var avoidBackupResources = map[string]bool{"pods": true}

func Register(
	ctx context.Context,
	backups backupControllers.BackupController,
	backupTemplates backupControllers.BackupTemplateController,
	clientSet *clientset.Clientset,
	dynamicInterface dynamic.Interface) {

	controller := &Handler{
		backups:         backups,
		backupTemplates: backupTemplates,
		discoveryClient: clientSet.Discovery(),
		dynamicClient:   dynamicInterface,
	}

	// Register handlers
	backups.OnChange(ctx, "backups", controller.OnBackupChange)
	//backups.OnRemove(ctx, controllerRemoveName, controller.OnEksConfigRemoved)
}

func (h *Handler) OnBackupChange(_ string, backup *v1.Backup) (*v1.Backup, error) {
	// TODO: get objectStore details too
	backupPath := backup.Spec.Local
	backupInfo, err := os.Stat(backupPath)
	if err == nil && backupInfo.IsDir() {
		return backup, nil
	}
	err = os.Mkdir(backupPath, os.ModePerm)
	if err != nil {
		return backup, fmt.Errorf("error creating temp dir: %v", err)
	}
	ownerDirPath := backupPath + "/owners"
	err = os.Mkdir(ownerDirPath, os.ModePerm)
	if err != nil {
		return backup, fmt.Errorf("error creating temp dir: %v", err)
	}
	dependentDirPath := backupPath + "/dependents"
	err = os.Mkdir(dependentDirPath, os.ModePerm)
	if err != nil {
		return backup, fmt.Errorf("error creating temp dir: %v", err)
	}
	//h.discoveryClient.ServerGroupsAndResources()

	template, err := h.backupTemplates.Get("default", backup.Spec.BackupTemplate, k8sv1.GetOptions{})
	if err != nil {
		return backup, err
	}
	err = h.gatherResources(template.BackupFilters, backupPath, ownerDirPath, dependentDirPath, &backup.Spec.BackupEncryptionConfig)

	filters, err := json.Marshal(template.BackupFilters)
	if err != nil {
		return backup, err
	}
	filterFile, err := os.Create(filepath.Join(backupPath, filepath.Base("filters.json")))
	if err != nil {
		return backup, fmt.Errorf("error creating filters file: %v", err)
	}
	defer filterFile.Close()
	if _, err := filterFile.Write(filters); err != nil {
		return backup, fmt.Errorf("error writing JSON to filters file: %v", err)
	}

	return backup, err
}

func (h *Handler) gatherResources(filters []v1.BackupFilter, backupPath, ownerDirPath, dependentDirPath string, encryptionConfig *v1.BackupEncryptionConfig) error {
	for ind, filter := range filters {
		resourceList, err := h.gatherResourcesForGroupVersion(filter)
		if err != nil {
			return err
		}
		if len(filter.Kinds) == 0 {
			// user gave kinds in regex, updated kinds as a list of resource names that exactly matched the regex
			for _, res := range resourceList {
				filter.Kinds = append(filter.Kinds, res.Name)
			}
			filters[ind] = filter
		}
		gv, err := schema.ParseGroupVersion(filter.ApiGroup)
		if err != nil {
			return err
		}

		fmt.Printf("\nBacking up resources for groupVersion %v\n", gv)
		fmt.Printf("\nres list: %v\n", resourceList)
		for _, res := range resourceList {
			fmt.Printf("\nresource: %v\n", res.Name)
			if skipBackup(res) {
				continue
			}
			err := h.gatherObjectsForResource(res, gv, filter, backupPath, ownerDirPath, dependentDirPath, encryptionConfig)
			if err != nil {
				fmt.Printf("\nerr in gatherObjectsForResource: %v\n", err)
				return err
			}
		}
	}
	return nil
}

func (h *Handler) gatherResourcesForGroupVersion(filter v1.BackupFilter) ([]k8sv1.APIResource, error) {
	fmt.Printf("\nHere-1\n")
	var resourceList []k8sv1.APIResource
	groupVersion := filter.ApiGroup

	resources, err := h.discoveryClient.ServerResourcesForGroupVersion(groupVersion)
	fmt.Printf("\nres list in gatherResourcesForGroupVersion: %v\n", resources)
	fmt.Printf("\nHere-2\n")
	if err != nil {
		return resourceList, err
	}

	// resources has all resources under given groupVersion, only user the ones in filter.Kinds
	if filter.KindsRegex == "." {
		// continue to retrieve everything
		resourceList = resources.APIResources
	} else {
		// else filter out resource with regex match
		for _, res := range resources.APIResources {
			if filter.ApiGroup == "apiextensions.k8s.io/v1" {
				fmt.Printf("\ncollecting resources for CRDs\n")
			}
			matched, err := regexp.MatchString(filter.KindsRegex, res.Name)
			if err != nil {
				return resourceList, err
			}
			if !matched {
				continue
			}
			fmt.Printf("\nres %v matched regex %v\n", res.Name, filter.KindsRegex)
			resourceList = append(resourceList, res)
		}
	}
	return resourceList, nil
}

func (h *Handler) gatherObjectsForResource(res k8sv1.APIResource, gv schema.GroupVersion, filter v1.BackupFilter, backupPath, ownerDirPath, dependentDirPath string, encryptionConfig *v1.BackupEncryptionConfig) error {
	var fieldSelector string
	gvr := gv.WithResource(res.Name)
	var dr dynamic.ResourceInterface
	dr = h.dynamicClient.Resource(gvr)

	// TODO: which context to use
	ctx := context.Background()
	// TODO: use single version to get consistent backup
	if len(filter.ResourceNames) > 0 {
		for _, ns := range filter.ResourceNames {
			fieldSelector += fmt.Sprintf("metadata.name=%s,", ns)
		}
	}
	if res.Namespaced {
		// filter based on namespaces if those fields are given
		if len(filter.Namespaces) > 0 {
			for _, ns := range filter.Namespaces {
				fieldSelector += fmt.Sprintf("metadata.namespace=%s,", ns)
			}
		}
	}
	strings.TrimRight(fieldSelector, ",")
	// resObjects are the objects from those namespaces and with those names after fieldSelectors applied
	resObjects, err := dr.List(ctx, k8sv1.ListOptions{FieldSelector: fieldSelector})
	if err != nil {
		return err
	}
	// check for regex
	var filteredObjects []unstructured.Unstructured
	if filter.ResourceNameRegex != "" {
		for _, resObj := range resObjects.Items {
			metadata := resObj.Object["metadata"].(map[string]interface{})
			name := metadata["name"].(string)
			nameMatched, err := regexp.MatchString(filter.ResourceNameRegex, name)
			if err != nil {
				return err
			}
			if !nameMatched {
				continue
			}
			filteredObjects = append(filteredObjects, resObj)
			fmt.Printf("\nMatched resource name %v for namesregex %v\n", name, filter.ResourceNameRegex)
		}
	}
	var filteredNs []string
	if filter.NamespaceRegex != "" {
		for _, resObj := range resObjects.Items {
			metadata := resObj.Object["metadata"].(map[string]interface{})
			namespace := metadata["namespace"].(string)
			nsMatched, err := regexp.MatchString(filter.NamespaceRegex, namespace)
			if err != nil {
				return err
			}
			if !nsMatched {
				continue
			}
			filteredObjects = append(filteredObjects, resObj)
			filteredNs = append(filteredNs, namespace)
			fmt.Printf("\nMatched resource ns %v for nsregex %v\n", namespace, filter.NamespaceRegex)
		}
	}
	if res.Namespaced && len(filteredNs) > 0 {
		filter.Namespaces = append(filter.Namespaces, filteredNs...)
	}
	if filter.NamespaceRegex == "" && filter.ResourceNameRegex == "" {
		// no regex, return all objects
		filteredObjects = resObjects.Items
	}

	return h.writeBackupObjects(filteredObjects, res, gv, backupPath, ownerDirPath, dependentDirPath, encryptionConfig)
}

func (h *Handler) writeBackupObjects(resObjects []unstructured.Unstructured, res k8sv1.APIResource, gv schema.GroupVersion, backupPath, ownerDirPath, dependentDirPath string, encryptionConfig *v1.BackupEncryptionConfig) error {
	for _, resObj := range resObjects {
		metadata := resObj.Object["metadata"].(map[string]interface{})
		// if an object has deletiontimestamp and finalizers, back it up. If there are no finalizers, ignore
		if _, deletionTs := metadata["deletionTimestamp"]; deletionTs {
			if _, finSet := metadata["finalizers"]; !finSet {
				// no finalizers set, don't backup object
				continue
			}
		}
		objName := resObj.Object["metadata"].(map[string]interface{})["name"].(string)
		if res.Name == "secrets" {
			secretMap := resObj.Object["data"].(map[string]interface{})
			encrypted, err := h.encryptSecrets(secretMap, encryptionConfig, objName)
			if err != nil {
				return err
			}
			resObj.Object["data"] = encrypted
		}
		currObjLabels := metadata["labels"]
		if resObj.Object["metadata"].(map[string]interface{})["uid"] != nil {
			oidLabel := map[string]string{common.OldUIDReferenceLabel: resObj.Object["metadata"].(map[string]interface{})["uid"].(string)}
			if currObjLabels == nil {
				metadata["labels"] = oidLabel
			} else {
				currLabels := currObjLabels.(map[string]interface{})
				currLabels[common.OldUIDReferenceLabel] = resObj.Object["metadata"].(map[string]interface{})["uid"].(string)
				metadata["labels"] = currLabels
			}
		}
		for _, field := range []string{"uid", "resourceVersion", "generation", "creationTimestamp"} {
			delete(metadata, field)
		}
		if res.Name == "customresourcedefinitions" || res.Name == "namespaces" {
			resourcePath := filepath.Join(backupPath, res.Name)
			if err := createResourceDir(resourcePath); err != nil {
				return err
			}
			err := writeToBackup(resObj.Object, resourcePath, objName)
			if err != nil {
				return err
			}
		}
		if resObj.Object["metadata"].(map[string]interface{})["ownerReferences"] == nil {
			resourcePath := ownerDirPath + "/" + res.Name + "." + gv.Group + "#" + gv.Version
			if err := createResourceDir(resourcePath); err != nil {
				return err
			}
			err := writeToBackup(resObj.Object, resourcePath, objName)
			if err != nil {
				return err
			}
		} else {
			resourcePath := dependentDirPath + "/" + res.Name + "." + gv.Group + "#" + gv.Version
			if err := createResourceDir(resourcePath); err != nil {
				return err
			}
			err := writeToBackup(resObj.Object, resourcePath, objName)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *Handler) encryptSecrets(secretMap map[string]interface{}, encryptionConfig *v1.BackupEncryptionConfig, objName string) ([]byte, error) {
	fmt.Printf("\necnrypt secret\n")
	var encryptionKey string
	var encrypted []byte
	secretPlaintext, err := json.Marshal(secretMap)
	if err != nil {
		return encrypted, err
	}
	if encryptionConfig.EncryptionSecret != "" {
		// either aescbc/gcm or secretbox, read secret
		encryptionKey, err = h.readEncryptionSecret(encryptionConfig.EncryptionSecret)
		if err != nil {
			return encrypted, err
		}
	}
	switch encryptionConfig.EncryptionProvider {
	case "aescbc":
		encrypted, err = encryptCBCMode([]byte(encryptionKey), secretPlaintext)
		if err != nil {
			return encrypted, err
		}
	case "aesgcm":
		encrypted, err = encryptGCMMode([]byte(encryptionKey), secretPlaintext, objName)
		if err != nil {
			return encrypted, err
		}
	case "secretbox":
		var key [32]byte
		copy(key[:], encryptionKey)
		encrypted, err = encryptSecretbox(key, secretPlaintext, objName)
		if err != nil {
			return encrypted, err
		}
	case "kms":
		KMSConfig := encryptionConfig.KMSConfiguration
		envelopeService, err := envelope.NewGRPCService(KMSConfig.Endpoint, KMSConfig.Timeout.Duration)
		if err != nil {
			return encrypted, fmt.Errorf("could not configure KMS plugin %q, error: %v", KMSConfig.PluginName, err)
		}
		envelopeTransformer, err := envelope.NewEnvelopeTransformer(envelopeService, int(*KMSConfig.CacheSize), k8saes.NewCBCTransformer)
		if err != nil {
			return encrypted, err
		}
		encrypted, err = envelopeTransformer.TransformToStorage(secretPlaintext, value.DefaultContext([]byte(objName)))
		if err != nil {
			return encrypted, err
		}
		//resObj.Object["data"] = encrypted
	}
	return encrypted, nil
}

func (h *Handler) readEncryptionSecret(aesSecretName string) (string, error) {
	secretsGV, err := schema.ParseGroupVersion("v1")
	if err != nil {
		return "", err
	}
	gvr := secretsGV.WithResource("secrets")
	secretsClient := h.dynamicClient.Resource(gvr)
	// TODO: accept secrets from different namespaces
	encryptionSecret, err := secretsClient.Namespace("default").Get(context.Background(), aesSecretName, k8sv1.GetOptions{})
	if err != nil {
		return "", err
	}
	encryptionKey := encryptionSecret.Object["data"].(map[string]interface{})["secret"].(string)
	return encryptionKey, nil
}

func encryptCBCMode(aesKeyBytes, secretPlaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(aesKeyBytes)
	if err != nil {
		return []byte{}, err
	}
	cbcTransformer := k8saes.NewCBCTransformer(block)
	encrypted, err := cbcTransformer.TransformToStorage(secretPlaintext, value.DefaultContext{})
	if err != nil {
		return []byte{}, err
	}
	return encrypted, nil
}

func encryptGCMMode(aesKeyBytes, secretPlaintext []byte, resourceName string) ([]byte, error) {
	block, err := aes.NewCipher(aesKeyBytes)
	if err != nil {
		return []byte{}, err
	}
	gcmTransformer := k8saes.NewGCMTransformer(block)
	encrypted, err := gcmTransformer.TransformToStorage(secretPlaintext, value.DefaultContext([]byte(resourceName)))
	if err != nil {
		return []byte{}, err
	}
	return encrypted, nil
}

func encryptSecretbox(secretboxBytes [32]byte, secretPlaintext []byte, resourceName string) ([]byte, error) {
	secretboxTransformer := secretbox.NewSecretboxTransformer(secretboxBytes)
	encrypted, err := secretboxTransformer.TransformToStorage(secretPlaintext, value.DefaultContext([]byte(resourceName)))
	if err != nil {
		return []byte{}, err
	}
	return encrypted, nil
}

func skipBackup(res k8sv1.APIResource) bool {
	if avoidBackupResources[res.Name] {
		return true
	}
	if !canListResource(res.Verbs) {
		fmt.Printf("\nCannot list resource %v\n", res)
		return true
	}
	if !canUpdateResource(res.Verbs) {
		fmt.Printf("\nCannot update resource %v\n", res)
		return true
	}
	return false
}

func createResourceDir(path string) error {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		err = os.Mkdir(path, os.ModePerm)
		if err != nil {
			return fmt.Errorf("error creating temp dir: %v", err)
		}
	}
	return nil
}

func writeToBackup(resource map[string]interface{}, backupPath, filename string) error {
	f, err := os.Create(filepath.Join(backupPath, filepath.Base(filename+".json")))
	if err != nil {
		return fmt.Errorf("error creating temp file: %v", err)
	}
	defer f.Close()

	resourceBytes, err := json.Marshal(resource)
	if err != nil {
		return fmt.Errorf("error converting resource to JSON: %v", err)
	}

	if _, err := f.Write(resourceBytes); err != nil {
		return fmt.Errorf("error writing JSON to file: %v", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("error closing file: %v", err)
	}
	return nil
}

func canListResource(verbs k8sv1.Verbs) bool {
	for _, v := range verbs {
		if v == "list" {
			return true
		}
	}
	return false
}

func canUpdateResource(verbs k8sv1.Verbs) bool {
	for _, v := range verbs {
		if v == "update" || v == "patch" {
			return true
		}
	}
	return false
}
