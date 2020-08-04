package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/rancher/wrangler/pkg/condition"
	"github.com/rancher/wrangler/pkg/slice"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	util "github.com/mrajashree/backup/pkg/controllers"
	backupControllers "github.com/mrajashree/backup/pkg/generated/controllers/backupper.cattle.io/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

type handler struct {
	ctx                     context.Context
	backups                 backupControllers.BackupController
	backupTemplates         backupControllers.BackupTemplateController
	backupEncryptionConfigs backupControllers.BackupEncryptionConfigController
	discoveryClient         discovery.DiscoveryInterface
	dynamicClient           dynamic.Interface
}

var avoidBackupResources = map[string]bool{"pods": true}

func Register(
	ctx context.Context,
	backups backupControllers.BackupController,
	backupTemplates backupControllers.BackupTemplateController,
	backupEncryptionConfigs backupControllers.BackupEncryptionConfigController,
	clientSet *clientset.Clientset,
	dynamicInterface dynamic.Interface) {

	controller := &handler{
		ctx:                     ctx,
		backups:                 backups,
		backupTemplates:         backupTemplates,
		backupEncryptionConfigs: backupEncryptionConfigs,
		discoveryClient:         clientSet.Discovery(),
		dynamicClient:           dynamicInterface,
	}

	// Register handlers
	backups.OnChange(ctx, "backups", controller.OnBackupChange)
}

func (h *handler) OnBackupChange(_ string, backup *v1.Backup) (*v1.Backup, error) {
	if condition.Cond(v1.BackupConditionReady).IsTrue(backup) && condition.Cond(v1.BackupConditionUploaded).IsTrue(backup) {
		return backup, nil
	}
	// empty dir defaults to os.TempDir
	tmpBackupPath, err := ioutil.TempDir("", backup.Spec.BackupFileName)
	if err != nil {
		return backup, fmt.Errorf("error creating temp dir: %v", err)
	}
	logrus.Infof("Temporary backup path is %v", tmpBackupPath)
	//h.discoveryClient.ServerGroupsAndResources()
	config, err := h.backupEncryptionConfigs.Get(backup.Spec.EncryptionConfigNamespace, backup.Spec.EncryptionConfigName, k8sv1.GetOptions{})
	if err != nil {
		if err := os.RemoveAll(tmpBackupPath); err != nil {
			return backup, err
		}
		return backup, err
	}
	transformerMap, err := util.GetEncryptionTransformers(config)
	if err != nil {
		if err := os.RemoveAll(tmpBackupPath); err != nil {
			return backup, err
		}
		return backup, err
	}

	template, err := h.backupTemplates.Get("default", backup.Spec.BackupTemplate, k8sv1.GetOptions{})
	if err != nil {
		if err := os.RemoveAll(tmpBackupPath); err != nil {
			return backup, err
		}
		return backup, err
	}
	err = h.gatherResources(template.BackupFilters, tmpBackupPath, transformerMap)
	if err != nil {
		if err := os.RemoveAll(tmpBackupPath); err != nil {
			return backup, err
		}
		return backup, err
	}
	filters, err := json.Marshal(template.BackupFilters)
	if err != nil {
		if err := os.RemoveAll(tmpBackupPath); err != nil {
			return backup, err
		}
		return backup, err
	}
	filterFile, err := os.Create(filepath.Join(tmpBackupPath, filepath.Base("filters.json")))
	if err != nil {
		if err := os.RemoveAll(tmpBackupPath); err != nil {
			return backup, err
		}
		return backup, fmt.Errorf("error creating filters file: %v", err)
	}
	defer filterFile.Close()
	if _, err := filterFile.Write(filters); err != nil {
		if err := os.RemoveAll(tmpBackupPath); err != nil {
			return backup, err
		}
		return backup, fmt.Errorf("error writing JSON to filters file: %v", err)
	}
	condition.Cond(v1.BackupConditionReady).SetStatusBool(backup, true)
	gzipFile := backup.Spec.BackupFileName + ".tar.gz"
	if backup.Spec.Local != "" {
		// for local, to send backup tar to given local path, use that as the path when creating compressed file
		if err := util.CreateTarAndGzip(tmpBackupPath, backup.Spec.Local, gzipFile); err != nil {
			if err := os.RemoveAll(tmpBackupPath); err != nil {
				return backup, err
			}
			return backup, err
		}
	} else if backup.Spec.ObjectStore != nil {
		if err := h.uploadToS3(backup, tmpBackupPath, gzipFile); err != nil {
			if err := os.RemoveAll(tmpBackupPath); err != nil {
				return backup, err
			}
			return backup, err
		}
	}
	condition.Cond(v1.BackupConditionUploaded).SetStatusBool(backup, true)
	if err := os.RemoveAll(tmpBackupPath); err != nil {
		return backup, err
	}
	if updBackup, err := h.backups.UpdateStatus(backup); err != nil {
		return updBackup, err
	}
	logrus.Infof("Done with backup")

	return backup, err
}

func (h *handler) gatherResources(filters []v1.BackupFilter, backupPath string, transformerMap map[schema.GroupResource]value.Transformer) error {
	for ind, filter := range filters {
		resourceList, err := h.gatherResourcesForGroupVersion(filter)
		if err != nil {
			return err
		}
		if len(filter.Kinds) == 0 {
			// user gave kinds in regex, updated kinds as a list of resource names that exactly matched the regex
			for _, res := range resourceList {
				if strings.Contains(res.Name, "/status") {
					// example "customresourcedefinitions/status"
					continue
				}
				filter.Kinds = append(filter.Kinds, res.Name)
			}
			filters[ind] = filter
		}
		gv, err := schema.ParseGroupVersion(filter.ApiGroup)
		if err != nil {
			return err
		}

		for _, res := range resourceList {
			if skipBackup(res) {
				continue
			}
			err := h.gatherObjectsForResource(res, gv, filter, backupPath, transformerMap)
			if err != nil {
				//fmt.Printf("\nerr in gatherObjectsForResource: %v\n", err)
				return err
			}
		}
	}
	return nil
}

func (h *handler) gatherResourcesForGroupVersion(filter v1.BackupFilter) ([]k8sv1.APIResource, error) {
	var resourceList []k8sv1.APIResource
	//var resources []*k8sv1.APIResourceList
	//var err error
	groupVersion := filter.ApiGroup

	//if groupVersion == "*" {
	//	_, resources, err = h.discoveryClient.ServerGroupsAndResources()
	//	if err != nil {
	//		return resourceList, err
	//	}
	//}
	resources, err := h.discoveryClient.ServerResourcesForGroupVersion(groupVersion)
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
			matched, err := regexp.MatchString(filter.KindsRegex, res.Name)
			if err != nil {
				return resourceList, err
			}
			if !matched {
				continue
			}
			logrus.Infof("resource kind %v matched regex %v\n", res.Name, filter.KindsRegex)
			resourceList = append(resourceList, res)
		}
	}

	// add the resources from Kinds field to this list
	if len(filter.Kinds) > 0 {
		resourceTypesToRetrieve := make(map[string]bool)
		for _, res := range resourceList {
			resourceTypesToRetrieve[res.Name] = true
		}
		for _, res := range resources.APIResources {
			if slice.ContainsString(filter.Kinds, res.Name) && !resourceTypesToRetrieve[res.Name] {
				resourceList = append(resourceList, res)
			}
		}
	}
	return resourceList, nil
}

func (h *handler) gatherObjectsForResource(res k8sv1.APIResource, gv schema.GroupVersion, filter v1.BackupFilter, backupPath string, transformerMap map[schema.GroupResource]value.Transformer) error {
	var fieldSelector string
	var filteredObjects []unstructured.Unstructured
	gvr := gv.WithResource(res.Name)
	var dr dynamic.ResourceInterface
	dr = h.dynamicClient.Resource(gvr)

	// if resource names or namespaces are provided, first filter using those as selectors
	if len(filter.ResourceNames) > 0 || len(filter.Namespaces) > 0 {
		for _, ns := range filter.ResourceNames {
			fieldSelector += fmt.Sprintf("metadata.name=%s,", ns)
		}
		for _, ns := range filter.Namespaces {
			fieldSelector += fmt.Sprintf("metadata.namespace=%s,", ns)
		}

		strings.TrimRight(fieldSelector, ",")
		filteredObjectsList, err := dr.List(h.ctx, k8sv1.ListOptions{FieldSelector: fieldSelector})
		if err != nil {
			return err
		}
		filteredObjects = filteredObjectsList.Items
	}

	// resourceVersion - if unset, Return data at the most recent resource version. The returned data must be consistent (i.e. served from etcd via a quorum read).
	resObjects, err := dr.List(h.ctx, k8sv1.ListOptions{FieldSelector: fieldSelector})
	if err != nil {
		return err
	}
	// check for regex
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
		}
	}
	if res.Namespaced && len(filteredNs) > 0 {
		filter.Namespaces = append(filter.Namespaces, filteredNs...)
	}
	if filter.NamespaceRegex == "" && filter.ResourceNameRegex == "" {
		// no regex, return all objects
		filteredObjects = resObjects.Items
	}

	return h.writeBackupObjects(filteredObjects, res, gv, backupPath, transformerMap)
}

func (h *handler) writeBackupObjects(resObjects []unstructured.Unstructured, res k8sv1.APIResource, gv schema.GroupVersion, backupPath string, transformerMap map[schema.GroupResource]value.Transformer) error {
	for _, resObj := range resObjects {
		metadata := resObj.Object["metadata"].(map[string]interface{})
		// if an object has deletiontimestamp and finalizers, back it up. If there are no finalizers, ignore
		if _, deletionTs := metadata["deletionTimestamp"]; deletionTs {
			if _, finSet := metadata["finalizers"]; !finSet {
				// no finalizers set, don't backup object
				continue
			}
		}

		currObjLabels := metadata["labels"]
		objName := metadata["name"].(string)
		if resObj.Object["metadata"].(map[string]interface{})["uid"] != nil {
			oidLabel := map[string]string{util.OldUIDReferenceLabel: resObj.Object["metadata"].(map[string]interface{})["uid"].(string)}
			if currObjLabels == nil {
				metadata["labels"] = oidLabel
			} else {
				currLabels := currObjLabels.(map[string]interface{})
				currLabels[util.OldUIDReferenceLabel] = resObj.Object["metadata"].(map[string]interface{})["uid"].(string)
				metadata["labels"] = currLabels
			}
		}

		for _, field := range []string{"uid", "generation", "creationTimestamp", "selfLink", "resourceVersion"} {
			delete(metadata, field)
		}

		gr := schema.ParseGroupResource(res.Name + "." + res.Group)
		encryptionTransformer := transformerMap[gr]
		additionalAuthenticatedData := objName
		//if res.Namespaced {
		//	additionalAuthenticatedData = metadata["namespace"].(string) + "/" + additionalAuthenticatedData
		//}

		resourcePath := backupPath + "/" + res.Name + "." + gv.Group + "#" + gv.Version
		if err := createResourceDir(resourcePath); err != nil {
			return err
		}

		err := writeToBackup(resObj.Object, resourcePath, objName, encryptionTransformer, additionalAuthenticatedData)
		if err != nil {
			return err
		}
	}
	return nil
}

func skipBackup(res k8sv1.APIResource) bool {
	if avoidBackupResources[res.Name] {
		return true
	}
	if !canListResource(res.Verbs) {
		logrus.Debugf("Cannot list resource %v, not backing up", res)
		return true
	}
	if !canUpdateResource(res.Verbs) {
		logrus.Debugf("Cannot update resource %v, not backing up\n", res)
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

func writeToBackup(resource map[string]interface{}, backupPath, filename string, transformer value.Transformer, additionalAuthenticatedData string) error {
	f, err := os.Create(filepath.Join(backupPath, filepath.Base(filename+".json")))
	if err != nil {
		return fmt.Errorf("error creating temp file: %v", err)
	}
	defer f.Close()

	resourceBytes, err := json.Marshal(resource)
	if err != nil {
		return fmt.Errorf("error converting resource to JSON: %v", err)
	}
	if transformer != nil {
		encrypted, err := transformer.TransformToStorage(resourceBytes, value.DefaultContext([]byte(additionalAuthenticatedData)))
		if err != nil {
			return fmt.Errorf("error converting resource to JSON: %v", err)
		}
		resourceBytes, err = json.Marshal(encrypted)
		if err != nil {
			return fmt.Errorf("error converting encrypted resource to JSON: %v", err)
		}
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
