package backup

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

type handler struct {
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

	controller := &handler{
		backups:         backups,
		backupTemplates: backupTemplates,
		discoveryClient: clientSet.Discovery(),
		dynamicClient:   dynamicInterface,
	}

	// Register handlers
	backups.OnChange(ctx, "backups", controller.OnBackupChange)
	//backups.OnRemove(ctx, controllerRemoveName, controller.OnEksConfigRemoved)
}

func (h *handler) OnBackupChange(_ string, backup *v1.Backup) (*v1.Backup, error) {
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
	aesKeySecretName := backup.Spec.BackupEncryptionSecretName
	secretsGV, err := schema.ParseGroupVersion("v1")
	if err != nil {
		return backup, err
	}
	gvr := secretsGV.WithResource("secrets")
	secretsClient := h.dynamicClient.Resource(gvr)
	// TODO: accept secrets from different namespaces
	aesSecret, err := secretsClient.Namespace("default").Get(context.Background(), aesKeySecretName, k8sv1.GetOptions{})
	if err != nil {
		return backup, err
	}
	aesKey := aesSecret.Object["data"].(map[string]interface{})["aeskey"].(string)
	template, err := h.backupTemplates.Get("default", backup.Spec.BackupTemplate, k8sv1.GetOptions{})
	if err != nil {
		return backup, err
	}
	err = h.gatherResources(template.BackupFilters, backupPath, ownerDirPath, dependentDirPath, aesKey)

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

func (h *handler) gatherResources(filters []v1.BackupFilter, backupPath, ownerDirPath, dependentDirPath, aesKey string) error {
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
			err := h.gatherObjectsForResource(res, gv, filter, backupPath, ownerDirPath, dependentDirPath, aesKey)
			if err != nil {
				fmt.Printf("\nerr in gatherObjectsForResource: %v\n", err)
				return err
			}
		}
	}
	return nil
}

func (h *handler) gatherResourcesForGroupVersion(filter v1.BackupFilter) ([]k8sv1.APIResource, error) {
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

func (h *handler) gatherObjectsForResource(res k8sv1.APIResource, gv schema.GroupVersion, filter v1.BackupFilter, backupPath, ownerDirPath, dependentDirPath, aesKey string) error {
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

	return writeBackupObjects(filteredObjects, res, gv, backupPath, ownerDirPath, dependentDirPath, aesKey)
}

func writeBackupObjects(resObjects []unstructured.Unstructured, res k8sv1.APIResource, gv schema.GroupVersion, backupPath, ownerDirPath, dependentDirPath, aesKey string) error {
	for _, resObj := range resObjects {
		metadata := resObj.Object["metadata"].(map[string]interface{})
		// if an object has deletiontimestamp and finalizers, back it up. If there are no finalizers, ignore
		if _, deletionTs := metadata["deletionTimestamp"]; deletionTs {
			if _, finSet := metadata["finalizers"]; !finSet {
				// no finalizers set, don't backup object
				continue
			}
		}
		if res.Name == "secrets" {
			fmt.Printf("\necnrypt secret\n")
			secretMap := resObj.Object["data"].(map[string]interface{})
			secretPlaintext, err := json.Marshal(secretMap)
			if err != nil {
				return err
			}
			block, _ := aes.NewCipher([]byte(aesKey))
			blockSize := aes.BlockSize
			paddingSize := blockSize - (len(secretPlaintext) % blockSize)
			result := make([]byte, blockSize+len(secretPlaintext)+paddingSize)
			iv := result[:blockSize]
			if _, err := io.ReadFull(rand.Reader, iv); err != nil {
				return fmt.Errorf("unable to read sufficient random bytes")
			}
			copy(result[blockSize:], secretPlaintext)

			// add PKCS#7 padding for CBC
			copy(result[blockSize+len(secretPlaintext):], bytes.Repeat([]byte{byte(paddingSize)}, paddingSize))

			mode := cipher.NewCBCEncrypter(block, iv)
			mode.CryptBlocks(result[blockSize:], result[blockSize:])
			resObj.Object["data"] = result
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
			err := writeToBackup(resObj.Object, resourcePath, resObj.Object["metadata"].(map[string]interface{})["name"].(string))
			if err != nil {
				return err
			}
		}
		if resObj.Object["metadata"].(map[string]interface{})["ownerReferences"] == nil {
			resourcePath := ownerDirPath + "/" + res.Name + "." + gv.Group + "#" + gv.Version
			if err := createResourceDir(resourcePath); err != nil {
				return err
			}
			err := writeToBackup(resObj.Object, resourcePath, resObj.Object["metadata"].(map[string]interface{})["name"].(string))
			if err != nil {
				return err
			}
		} else {
			resourcePath := dependentDirPath + "/" + res.Name + "." + gv.Group + "#" + gv.Version
			if err := createResourceDir(resourcePath); err != nil {
				return err
			}
			err := writeToBackup(resObj.Object, resourcePath, resObj.Object["metadata"].(map[string]interface{})["name"].(string))
			if err != nil {
				return err
			}
		}
	}
	return nil
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
