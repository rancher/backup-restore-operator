package restore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	util "github.com/mrajashree/backup/pkg/controllers"
	backupControllers "github.com/mrajashree/backup/pkg/generated/controllers/backupper.cattle.io/v1"
	lasso "github.com/rancher/lasso/pkg/client"
	"github.com/rancher/wrangler/pkg/slice"
	"github.com/sirupsen/logrus"

	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	//"k8s.io/apimachinery/pkg/api/meta"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

const (
	metadataMapKey  = "metadata"
	ownerRefsMapKey = "ownerReferences"
)

type handler struct {
	ctx                     context.Context
	restores                backupControllers.RestoreController
	backups                 backupControllers.BackupController
	backupEncryptionConfigs backupControllers.BackupEncryptionConfigController
	discoveryClient         discovery.DiscoveryInterface
	dynamicClient           dynamic.Interface
	sharedClientFactory     lasso.SharedClientFactory
}

type restoreObj struct {
	Name               string
	Namespace          string
	GVR                schema.GroupVersionResource
	ResourceConfigPath string
	Data               *unstructured.Unstructured
}

//var RestoreObjCreated = make(map[types.UID]map[*restoreObj]bool)
//var RestoreObjAdjacencyList = make(map[types.UID]map[*restoreObj][]restoreObj)

func Register(
	ctx context.Context,
	restores backupControllers.RestoreController,
	backups backupControllers.BackupController,
	backupEncryptionConfigs backupControllers.BackupEncryptionConfigController,
	clientSet *clientset.Clientset,
	dynamicInterface dynamic.Interface,
	sharedClientFactory lasso.SharedClientFactory) {

	controller := &handler{
		ctx:                     ctx,
		restores:                restores,
		backups:                 backups,
		backupEncryptionConfigs: backupEncryptionConfigs,
		dynamicClient:           dynamicInterface,
		discoveryClient:         clientSet.Discovery(),
		sharedClientFactory:     sharedClientFactory,
	}

	// Register handlers
	restores.OnChange(ctx, "restore", controller.OnRestoreChange)
}

func (h *handler) OnRestoreChange(_ string, restore *v1.Restore) (*v1.Restore, error) {
	created := make(map[string]bool)
	ownerToDependentsList := make(map[string][]restoreObj)
	var toRestore []restoreObj
	numOwnerReferences := make(map[string]int)

	backupName := restore.Spec.BackupFileName

	backupPath, err := ioutil.TempDir("", strings.TrimSuffix(backupName, ".tar.gz"))
	if err != nil {
		return restore, err
	}
	logrus.Infof("Temporary path for un-tar/gzip backup data during restore: %v", backupPath)

	if restore.Spec.Local != "" {
		// if local, backup tar.gz must be added to the "Local" path
		backupFilePath := filepath.Join(restore.Spec.Local, backupName)
		if err := util.LoadFromTarGzip(backupFilePath, backupPath); err != nil {
			removeDirErr := os.RemoveAll(backupPath)
			if removeDirErr != nil {
				return restore, errors.New(err.Error() + removeDirErr.Error())
			}
			return restore, err
		}
	} else if restore.Spec.ObjectStore != nil {
		backupFilePath, err := h.downloadFromS3(restore)
		if err != nil {
			removeDirErr := os.RemoveAll(backupPath)
			if removeDirErr != nil {
				return restore, errors.New(err.Error() + removeDirErr.Error())
			}
			removeFileErr := os.Remove(backupFilePath)
			if removeFileErr != nil {
				return restore, errors.New(err.Error() + removeFileErr.Error())
			}
			return restore, err
		}
		if err := util.LoadFromTarGzip(backupFilePath, backupPath); err != nil {
			removeDirErr := os.RemoveAll(backupPath)
			if removeDirErr != nil {
				return restore, errors.New(err.Error() + removeDirErr.Error())
			}
			removeFileErr := os.Remove(backupFilePath)
			if removeFileErr != nil {
				return restore, errors.New(err.Error() + removeFileErr.Error())
			}
			return restore, err
		}
		// remove the downloaded gzip file from s3 as contents are untar/unzipped at the temp location by this point
		removeFileErr := os.Remove(backupFilePath)
		if removeFileErr != nil {
			return restore, errors.New(err.Error() + removeFileErr.Error())
		}
	}
	backupPath = strings.TrimSuffix(backupPath, ".tar.gz")
	logrus.Infof("Untar/Ungzip backup at %v", backupPath)
	config, err := h.backupEncryptionConfigs.Get(restore.Spec.EncryptionConfigNamespace, restore.Spec.EncryptionConfigName, k8sv1.GetOptions{})
	if err != nil {
		removeDirErr := os.RemoveAll(backupPath)
		if removeDirErr != nil {
			return restore, errors.New(err.Error() + removeDirErr.Error())
		}
		return restore, err
	}
	transformerMap, err := util.GetEncryptionTransformers(config)
	if err != nil {
		removeDirErr := os.RemoveAll(backupPath)
		if removeDirErr != nil {
			return restore, errors.New(err.Error() + removeDirErr.Error())
		}
		return restore, err
	}

	// first restore CRDs
	//_, err = os.Stat(filepath.Join(backupPath, "customresourcedefinitions.apiextensions.k8s.io#v1"))
	//if err == nil {
	startTime := time.Now()
	fmt.Printf("\nStart time: %v\n", startTime)
	if err := h.restoreCRDs(backupPath, "customresourcedefinitions.apiextensions.k8s.io#v1", transformerMap, created, true); err != nil {
		removeDirErr := os.RemoveAll(backupPath)
		if removeDirErr != nil {
			return restore, errors.New(err.Error() + removeDirErr.Error())
		}
		return restore, err
	}
	timeForRestoringCRDs := time.Since(startTime)
	fmt.Printf("\ntime taken to restore CRDs: %v\n", timeForRestoringCRDs)
	doneRestoringCRDTime := time.Now()
	//}
	//
	//os.Stat(filepath.Join(backupPath, "customresourcedefinitions.apiextensions.k8s.io#v1beta1"))

	// generate adjacency lists for dependents and ownerRefs
	if err := h.generateDependencyGraph(backupPath, transformerMap, ownerToDependentsList, &toRestore, numOwnerReferences); err != nil {
		removeDirErr := os.RemoveAll(backupPath)
		if removeDirErr != nil {
			return restore, errors.New(err.Error() + removeDirErr.Error())
		}
		return restore, err
	}
	timeForGeneratingGraph := time.Since(doneRestoringCRDTime)
	fmt.Printf("\ntime taken to generate graph: %v\n", timeForGeneratingGraph)
	doneGeneratingGraphTime := time.Now()
	logrus.Infof("No-goroutines-2 time right before starting to create from graph: %v", doneGeneratingGraphTime)
	if err := h.createFromDependencyGraph(ownerToDependentsList, created, numOwnerReferences, toRestore); err != nil {
		removeDirErr := os.RemoveAll(backupPath)
		if removeDirErr != nil {
			return restore, errors.New(err.Error() + removeDirErr.Error())
		}
		return restore, err
	}
	timeForRestoringResources := time.Since(doneGeneratingGraphTime)
	fmt.Printf("\ntime taken to restore resources: %v\n", timeForRestoringResources)
	err = os.RemoveAll(backupPath)
	return restore, err

	//if dependentRestoreErr != nil {
	//	return restore, dependentRestoreErr
	//}

	if err := h.prune(backupPath, restore.Spec.ForcePruneTimeout); err != nil {
		return restore, fmt.Errorf("error pruning during restore")
	}
	logrus.Infof("Done restoring")
	if err := os.RemoveAll(backupPath); err != nil {
		return restore, err
	}
	return restore, nil
}

func (h *handler) restoreCRDs(backupPath, resourceGVK string, transformerMap map[schema.GroupResource]value.Transformer, created map[string]bool, crdApiVersionV1 bool) error {
	resourceDirPath := path.Join(backupPath, resourceGVK)
	gvr := getGVR(resourceGVK)
	gr := gvr.GroupResource()
	decryptionTransformer, _ := transformerMap[gr]
	dirContents, err := ioutil.ReadDir(resourceDirPath)
	if err != nil {
		return err
	}
	for _, resFile := range dirContents {
		resConfigPath := filepath.Join(resourceDirPath, resFile.Name())
		//if crdApiVersionV1 {
		//	resBytes, err := ioutil.ReadFile(resConfigPath)
		//	if err != nil {
		//		fmt.Printf("\nerr readin file %v: %v\n", resConfigPath, err)
		//		return err
		//	}
		//
		//	fmt.Printf("\nread file %v\n", string(resBytes))
		//	if decryptionTransformer != nil {
		//		var encryptedBytes []byte
		//		if err := json.Unmarshal(resBytes, &encryptedBytes); err != nil {
		//			return err
		//		}
		//		decrypted, _, err := decryptionTransformer.TransformFromStorage(encryptedBytes, value.DefaultContext(resFile.Name()))
		//		if err != nil {
		//			return err
		//		}
		//		resBytes = decrypted
		//	}
		//	var fileMap map[string]interface{}
		//	err = json.Unmarshal(resBytes, &fileMap)
		//	if err != nil {
		//		fmt.Printf("\nerr Unmarshal file %v: %v\n", resConfigPath, err)
		//		return err
		//	}
		//	//spec := fileMap["spec"].(map[string]interface{})
		//	//if preserveUnknownFields, ok := spec["preserveUnknownFields"].(bool); ok && preserveUnknownFields {
		//	//	spec["preserveUnknownFields"] = false
		//	//}
		//	//fileMap["spec"] = spec
		//	fileMap["apiVersion"] = "apiextensions.k8s.io/v1beta1"
		//	writeBytes, err := json.Marshal(fileMap)
		//	if err != nil {
		//		fmt.Printf("\nerr marshal file %v: %v\n", resConfigPath, err)
		//		return fmt.Errorf("error marshaling updated ownerRefs: %v", err)
		//	}
		//	fmt.Printf("\nwriting to file: %v\n", string(writeBytes))
		//	if err := ioutil.WriteFile(resConfigPath, writeBytes, 0777); err != nil {
		//		fmt.Printf("\nerr WriteFile file %v: %v\n", resConfigPath, err)
		//		return err
		//	}
		//}
		crdContent, err := ioutil.ReadFile(resConfigPath)
		if err != nil {
			return err
		}
		var crdData map[string]interface{}
		if err := json.Unmarshal(crdContent, &crdData); err != nil {
			return err
		}
		crdName := strings.TrimSuffix(resFile.Name(), ".json")
		if decryptionTransformer != nil {
			var encryptedBytes []byte
			if err := json.Unmarshal(crdContent, &encryptedBytes); err != nil {
				return err
			}
			decrypted, _, err := decryptionTransformer.TransformFromStorage(encryptedBytes, value.DefaultContext(crdName))
			if err != nil {
				return err
			}
			crdContent = decrypted
		}
		restoreObjKey := restoreObj{
			Name:               crdName,
			ResourceConfigPath: resConfigPath,
			GVR:                gvr,
			Data:               &unstructured.Unstructured{Object: crdData},
		}
		err = h.restoreResource(restoreObjKey, gvr)
		if err != nil {
			return fmt.Errorf("restoreCRDs: %v", err)
		}

		created[restoreObjKey.ResourceConfigPath] = true
	}
	return nil
}

func (h *handler) generateDependencyGraph(backupPath string, transformerMap map[schema.GroupResource]value.Transformer,
	ownerToDependentsList map[string][]restoreObj, toRestore *[]restoreObj, numOwnerReferences map[string]int) error {
	backupEntries, err := ioutil.ReadDir(backupPath)
	if err != nil {
		return err
	}

	for _, backupEntry := range backupEntries {
		if backupEntry.Name() == "filters" {
			continue
		}

		// example catalogs.management.cattle.io#v3
		resourceGVK := backupEntry.Name()
		resourceDirPath := path.Join(backupPath, resourceGVK)
		gvr := getGVR(resourceGVK)
		gr := gvr.GroupResource()
		resourceFiles, err := ioutil.ReadDir(resourceDirPath)
		if err != nil {
			return err
		}

		for _, resourceFile := range resourceFiles {
			resManifestPath := filepath.Join(resourceDirPath, resourceFile.Name())
			if err := h.addToOwnersToDependentsList(backupPath, resManifestPath, resourceFile.Name(), gvr, transformerMap[gr],
				ownerToDependentsList, toRestore, numOwnerReferences); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *handler) addToOwnersToDependentsList(backupPath, resConfigPath, aad string, gvr schema.GroupVersionResource, decryptionTransformer value.Transformer,
	ownerToDependentsList map[string][]restoreObj, toRestore *[]restoreObj, numOwnerReferences map[string]int) error {
	logrus.Infof("Processing %v for adjacency list", resConfigPath)
	resBytes, err := ioutil.ReadFile(resConfigPath)
	if err != nil {
		return err
	}

	if decryptionTransformer != nil {
		var encryptedBytes []byte
		if err := json.Unmarshal(resBytes, &encryptedBytes); err != nil {
			return err
		}
		decrypted, _, err := decryptionTransformer.TransformFromStorage(encryptedBytes, value.DefaultContext(aad))
		if err != nil {
			return err
		}
		resBytes = decrypted
	}

	fileMap := make(map[string]interface{})
	err = json.Unmarshal(resBytes, &fileMap)
	if err != nil {
		return err
	}

	metadata, metadataFound := fileMap[metadataMapKey].(map[string]interface{})
	if !metadataFound {
		return nil
	}

	// add to adjacency list
	name, _ := metadata["name"].(string)
	namespace, isNamespaced := metadata["namespace"].(string)
	currRestoreObj := restoreObj{
		Name:               name,
		ResourceConfigPath: resConfigPath,
		GVR:                gvr,
		Data:               &unstructured.Unstructured{Object: fileMap},
	}
	if isNamespaced {
		currRestoreObj.Namespace = namespace
	}

	ownerRefs, ownerRefsFound := metadata[ownerRefsMapKey].([]interface{})
	if !ownerRefsFound {
		// has no dependents, so no need to add to adjacency list, add to restoreResources list
		*toRestore = append(*toRestore, currRestoreObj)
		return nil
	}
	numOwners := 0
	for _, owner := range ownerRefs {
		numOwners++
		ownerRefData, ok := owner.(map[string]interface{})
		if !ok {
			logrus.Errorf("invalid ownerRef")
			continue
		}

		groupVersion := ownerRefData["apiVersion"].(string)
		gv, err := schema.ParseGroupVersion(groupVersion)
		if err != nil {
			logrus.Errorf(" err %v parsing ownerRef apiVersion", err)
			continue
		}
		kind := ownerRefData["kind"].(string)
		gvk := gv.WithKind(kind)
		ownerGVR, isNamespaced, err := h.sharedClientFactory.ResourceForGVK(gvk)
		if err != nil {
			return fmt.Errorf("Error getting resource for gvk %v: %v", gvk, err)
		}

		var apiGroup, version string
		split := strings.SplitN(groupVersion, "/", 2)
		if len(split) == 1 {
			// resources under v1 version
			version = split[0]
		} else {
			apiGroup = split[0]
			version = split[1]
		}
		// TODO: check if this object creation is needed
		// kind + "." + apigroup + "#" + version
		ownerDirPath := fmt.Sprintf("%s.%s#%s", ownerGVR.Resource, apiGroup, version)
		ownerName := ownerRefData["name"].(string)
		// Store resourceConfigPath of owner Ref because that's what we check for in "Created" map
		ownerObj := restoreObj{
			Name:               ownerName,
			ResourceConfigPath: filepath.Join(backupPath, ownerDirPath, ownerName+".json"),
			GVR:                ownerGVR,
		}
		if isNamespaced {
			// if owning object is namespaced, then it has to be the same ns as the current dependent object
			ownerObj.Namespace = currRestoreObj.Namespace
		}
		ownerObjDependents, ok := ownerToDependentsList[ownerObj.ResourceConfigPath]
		if !ok {
			ownerToDependentsList[ownerObj.ResourceConfigPath] = []restoreObj{currRestoreObj}
		} else {
			ownerToDependentsList[ownerObj.ResourceConfigPath] = append(ownerObjDependents, currRestoreObj)
		}
	}

	numOwnerReferences[currRestoreObj.ResourceConfigPath] = numOwners
	return nil
}

func (h *handler) createFromDependencyGraph(ownerToDependentsList map[string][]restoreObj, created map[string]bool,
	numOwnerReferences map[string]int, toRestore []restoreObj) error {
	numTotalDependents := 0
	for _, dependents := range ownerToDependentsList {
		numTotalDependents += len(dependents)
	}
	countRestored := 0
	var errList []error
	for len(toRestore) > 0 {
		curr := toRestore[0]
		if len(toRestore) == 1 {
			toRestore = []restoreObj{}
		} else {
			toRestore = toRestore[1:]
		}
		if created[curr.ResourceConfigPath] {
			logrus.Infof("Resource %v is already created", curr.ResourceConfigPath)
			continue
		}
		if err := h.restoreResource(curr, curr.GVR); err != nil {
			errList = append(errList, err)
			continue
		}
		for _, dependent := range ownerToDependentsList[curr.ResourceConfigPath] {
			// example, curr = catTemplate, dependent=catTempVer
			if numOwnerReferences[dependent.ResourceConfigPath] > 0 {
				numOwnerReferences[dependent.ResourceConfigPath]--
			}
			if numOwnerReferences[dependent.ResourceConfigPath] == 0 {
				logrus.Infof("dependent %v is now ready to create", dependent.Name)
				toRestore = append(toRestore, dependent)
			}
		}
		created[curr.ResourceConfigPath] = true
		countRestored++
	}
	fmt.Printf("\nTotal restored resources final: %v\n", countRestored)
	return util.ErrList(errList)
}

func (h *handler) updateOwnerRefs(ownerReferences []interface{}, namespace string) error {
	for ind, ownerRef := range ownerReferences {
		reference := ownerRef.(map[string]interface{})
		apiversion, _ := reference["apiVersion"].(string)
		kind, _ := reference["kind"].(string)
		if apiversion == "" || kind == "" {
			continue
		}
		ownerGV, err := schema.ParseGroupVersion(apiversion)
		if err != nil {
			return fmt.Errorf("err %v parsing apiversion %v", err, apiversion)
		}
		ownerGVK := ownerGV.WithKind(kind)
		name, _ := reference["name"].(string)

		ownerGVR, isNamespaced, err := h.sharedClientFactory.ResourceForGVK(ownerGVK)
		if err != nil {
			return fmt.Errorf("error getting resource for gvk %v: %v", ownerGVK, err)
		}
		ownerObj := &restoreObj{
			Name: name,
			GVR:  ownerGVR,
		}
		// if owner object is namespaced, it has to be within same namespace, since per definition
		/*
			// OwnerReference contains enough information to let you identify an owning
			// object. An owning object must be in the same namespace as the dependent, or
			// be cluster-scoped, so there is no namespace field.*/
		if isNamespaced {
			ownerObj.Namespace = namespace
		}

		logrus.Infof("Getting new UID for %v ", ownerObj.Name)
		ownerObjNewUID, err := h.getOwnerNewUID(ownerObj)
		if err != nil {
			return fmt.Errorf("error obtaining new UID for %v: %v", ownerObj.Name, err)
		}
		reference["uid"] = ownerObjNewUID
		ownerReferences[ind] = reference
	}
	return nil
}

func (h *handler) restoreResource(currRestoreObj restoreObj, gvr schema.GroupVersionResource) error {
	logrus.Infof("Restoring %v", currRestoreObj.Name)

	fileMap := currRestoreObj.Data.Object
	obj := currRestoreObj.Data

	fileMapMetadata := fileMap[metadataMapKey].(map[string]interface{})
	name := fileMapMetadata["name"].(string)
	namespace, _ := fileMapMetadata["namespace"].(string)
	var dr dynamic.ResourceInterface
	dr = h.dynamicClient.Resource(gvr)
	if namespace != "" {
		dr = h.dynamicClient.Resource(gvr).Namespace(namespace)
	}
	ownerReferences, _ := fileMapMetadata[ownerRefsMapKey].([]interface{})
	if ownerReferences != nil {
		if err := h.updateOwnerRefs(ownerReferences, namespace); err != nil {
			return err
		}
	}

	res, err := dr.Get(h.ctx, name, k8sv1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("restoreResource: err getting resource %v", err)
		}
		// create and return
		_, err := dr.Create(h.ctx, obj, k8sv1.CreateOptions{})
		if err != nil {
			return err
		}
		return nil
	}
	resMetadata := res.Object[metadataMapKey].(map[string]interface{})
	resourceVersion := resMetadata["resourceVersion"].(string)
	obj.Object[metadataMapKey].(map[string]interface{})["resourceVersion"] = resourceVersion
	_, err = dr.Update(h.ctx, obj, k8sv1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("restoreResource: err updating resource %v", err)
	}
	_, hasStatusSubresource := res.Object["status"]
	if hasStatusSubresource {
		_, err := dr.UpdateStatus(h.ctx, obj, k8sv1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("restoreResource: err updating status resource %v", err)
		}
	}

	fmt.Printf("\nSuccessfully restored %v\n", name)
	return nil
}

func (h *handler) getOwnerNewUID(owner *restoreObj) (string, error) {
	var ownerDyn dynamic.ResourceInterface
	ownerDyn = h.dynamicClient.Resource(owner.GVR)

	if owner.Namespace != "" {
		ownerDyn = h.dynamicClient.Resource(owner.GVR).Namespace(owner.Namespace)
	}
	ownerObj, err := ownerDyn.Get(h.ctx, owner.Name, k8sv1.GetOptions{})
	if err != nil {
		return "", err
	}
	ownerObjMetadata := ownerObj.Object[metadataMapKey].(map[string]interface{})
	ownerObjUID := ownerObjMetadata["uid"].(string)
	return ownerObjUID, nil
}

func getGVR(resourceGVK string) schema.GroupVersionResource {
	gvkParts := strings.Split(resourceGVK, "#")
	version := gvkParts[1]
	resourceGroup := strings.SplitN(gvkParts[0], ".", 2)
	resource := strings.TrimSuffix(resourceGroup[0], ".")
	var group string
	if len(resourceGroup) > 1 {
		group = resourceGroup[1]
	}
	gr := schema.ParseGroupResource(resource + "." + group)
	gvr := gr.WithVersion(version)
	return gvr
}

func (h *handler) prune(backupPath string, pruneTimeout int) error {
	// prune
	filtersBytes, err := ioutil.ReadFile(filepath.Join(backupPath, "filters.json"))
	if err != nil {
		return fmt.Errorf("error reading backup fitlers file: %v", err)
	}
	var backupFilters []v1.BackupFilter
	if err := json.Unmarshal(filtersBytes, &backupFilters); err != nil {
		return fmt.Errorf("error unmarshaling backup filters file: %v", err)
	}
	resourceToPrune := make(map[string]map[string]bool)
	resourceToPruneNamespaced := make(map[string]map[string]bool)
	for _, filter := range backupFilters {
		groupVersion := filter.ApiGroup
		resources := filter.Kinds
		for _, res := range resources {
			var fieldSelector string
			var filteredObjects []unstructured.Unstructured

			gv, _ := schema.ParseGroupVersion(groupVersion)
			gvr := gv.WithResource(res)
			var dr dynamic.ResourceInterface
			dr = h.dynamicClient.Resource(gvr)

			// filter based on namespaces if given
			if len(filter.Namespaces) > 0 {
				for _, ns := range filter.Namespaces {
					fieldSelector += fmt.Sprintf("metadata.namespace=%s,", ns)
				}
			}

			resObjects, err := dr.List(context.Background(), k8sv1.ListOptions{FieldSelector: fieldSelector})
			if err != nil {
				return err
			}

			if filter.NamespaceRegex != "" {
				for _, resObj := range resObjects.Items {
					metadata := resObj.Object["metadata"].(map[string]interface{})
					namespace := metadata["namespace"].(string)
					nsMatched, err := regexp.MatchString(filter.NamespaceRegex, namespace)
					if err != nil {
						return err
					}
					if !nsMatched {
						// resource does not match up to filter, ignore it
						continue
					}
					filteredObjects = append(filteredObjects, resObj)
				}
			} else {
				filteredObjects = resObjects.Items
			}

			for _, obj := range filteredObjects {
				metadata := obj.Object["metadata"].(map[string]interface{})
				name := metadata["name"].(string)
				namespace, _ := metadata["namespace"].(string)
				// resource doesn't match to this filter, so ignore it
				// TODO: check if exact name match logic makes sense
				if len(filter.ResourceNames) > 0 && !slice.ContainsString(filter.ResourceNames, name) {
					continue
				}
				if filter.ResourceNameRegex != "" {
					nameMatched, err := regexp.MatchString(filter.ResourceNameRegex, name)
					if err != nil {
						return err
					}
					// resource doesn't match to this filter, so ignore it
					// for instance for rancher, we want to inlude all p-xxxx ns, so if the ns is totally different, ignore it
					if !nameMatched {
						continue
					}
				}

				// resource matches to this filter, check if it exists in the backup
				// first check if it's a CRD or a namespace
				var backupObjectPaths []string
				for _, path := range []string{"customresourcedefinitions", "namespaces"} {
					objBackupPath := filepath.Join(backupPath, path)
					backupObjectPaths = append(backupObjectPaths, objBackupPath)
				}
				for _, path := range []string{"owners", "dependents"} {
					objBackupPath := filepath.Join(backupPath, path, res+"."+gv.Group+"#"+gv.Version)
					backupObjectPaths = append(backupObjectPaths, objBackupPath)
				}
				exists := false
				for _, path := range backupObjectPaths {
					fileName := name + ".json"
					_, err := os.Stat(filepath.Join(path, fileName))
					if err == nil || os.IsExist(err) {
						exists = true
						// check if this resource was marked for deletion for a previous filter
						if namespace != "" {
							if _, ok := resourceToPruneNamespaced[res][namespace+"/"+name]; ok {
								// remove it from the map of resources to be pruned
								delete(resourceToPruneNamespaced[res], namespace+"/"+name)
							}
						} else {
							if _, ok := resourceToPrune[res][name]; ok {
								delete(resourceToPrune[res], name)
							}
						}
						continue
					}
				}
				if !exists {
					if namespace != "" {
						if _, ok := resourceToPruneNamespaced[res][namespace+"/"+name]; !ok {
							resourceToPruneNamespaced[res] = map[string]bool{namespace + "/" + name: true}
						}

					} else {
						if _, ok := resourceToPrune[res][name]; !ok {
							resourceToPrune[res] = map[string]bool{name: true}
						}
					}
				}
			}
			logrus.Infof("done gathering prune resourceNames for res %v", res)
		}
	}

	fmt.Printf("\nneed to delete following resources: %v\n", resourceToPrune)
	fmt.Printf("\nneed to delete following namespaced resources: %v\n", resourceToPruneNamespaced)
	go func(ctx context.Context, resourceToPrune map[string]map[string]bool, resourceToPruneNamespaced map[string]map[string]bool, pruneTimeout int) {
		// workers group parallel deletion
		deleteResources(resourceToPrune, resourceToPruneNamespaced, false)
		logrus.Infof("Done trying delete -1")
		//time.Sleep(time.Duration(pruneTimeout) * time.Second)
		time.Sleep(2 * time.Second)
		logrus.Infof("Ensuring resources to prune are deleted")
		deleteResources(resourceToPrune, resourceToPruneNamespaced, true)
	}(h.ctx, resourceToPrune, resourceToPruneNamespaced, pruneTimeout)
	return nil
}

func deleteResources(resourceToPrune map[string]map[string]bool, resourceToPruneNamespaced map[string]map[string]bool, removeFinalizers bool) {
	for resource, names := range resourceToPrune {
		for name := range names {
			if removeFinalizers {
				getCMDOp, err := exec.Command("kubectl", "get", resource, name).CombinedOutput()
				if err != nil && strings.Contains(string(getCMDOp), "NotFound") {
					logrus.Infof("Error getting %v, must have been deleted", name)
					continue
				}
				logrus.Infof("Removing finalizer from %v", name)
				removeFinalizerOp, err := exec.Command("kubectl", "patch", resource, name, "-p", `{"metadata":{"finalizers":null}}`).CombinedOutput()
				if err != nil {
					logrus.Errorf("Error removing finalizer on %v: %v", name, string(removeFinalizerOp)+err.Error())
					continue
				}
				logrus.Infof("Removed finalizer from %v", name)
			}
			out, err := exec.Command("kubectl", "delete", resource, name).CombinedOutput()
			if err != nil {
				if strings.Contains(string(out), "NotFound") {
					logrus.Debugf("Resource %v already deleted", name)
					continue
				}
				logrus.Errorf("\nError deleting resource %v: %v\n", name, string(out)+"\n"+err.Error())
			}
		}
	}

	for resource, names := range resourceToPruneNamespaced {
		for nsName := range names {
			split := strings.SplitN(nsName, "/", 2)
			ns := split[0]
			name := split[1]
			if removeFinalizers {
				getCMDOp, err := exec.Command("kubectl", "get", resource, name, "-n", ns).CombinedOutput()
				if err != nil && strings.Contains(string(getCMDOp), "NotFound") {
					logrus.Infof("Error getting %v from namespace %v, must have been deleted", name, ns)
					continue
				}
				logrus.Infof("Removing finalizer from %v", name)
				removeFinalizerOp, err := exec.Command("kubectl", "patch", resource, name, "-p", `{"metadata":{"finalizers":null}}`, "-n", ns).CombinedOutput()
				if err != nil {
					logrus.Errorf("Error removing finalizer on %v: %v", name, string(removeFinalizerOp)+err.Error())
					continue
				}
				logrus.Infof("Removed finalizer from %v", name)
			}
			// not supposed to be here, so delete this resource
			out, err := exec.Command("kubectl", "delete", resource, name, "-n", ns).CombinedOutput()
			if err != nil {
				if strings.Contains(string(out), "NotFound") {
					logrus.Debugf("Resource %v already deleted", name)
					continue
				}
				logrus.Errorf("\nError deleting namespaced resource %v: %v\n", name, string(out)+"\n"+err.Error())
			}
		}
	}
}
