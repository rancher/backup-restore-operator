package restore

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"k8s.io/client-go/restmapper"

	//"k8s.io/apimachinery/pkg/api/meta"
	//"k8s.io/client-go/restmapper"
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
	"github.com/rancher/wrangler/pkg/slice"
	"github.com/sirupsen/logrus"

	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/discovery/cached/memory"
	//"k8s.io/apimachinery/pkg/api/meta"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	//"k8s.io/client-go/restmapper"
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
}

type restoreObj struct {
	Name               string
	Namespace          string
	GVR                schema.GroupVersionResource
	ResourceConfigPath string
}

//var RestoreObjCreated = make(map[types.UID]map[*restoreObj]bool)
//var RestoreObjAdjacencyList = make(map[types.UID]map[*restoreObj][]restoreObj)

func Register(
	ctx context.Context,
	restores backupControllers.RestoreController,
	backups backupControllers.BackupController,
	backupEncryptionConfigs backupControllers.BackupEncryptionConfigController,
	clientSet *clientset.Clientset,
	dynamicInterface dynamic.Interface) {

	controller := &handler{
		ctx:                     ctx,
		restores:                restores,
		backups:                 backups,
		backupEncryptionConfigs: backupEncryptionConfigs,
		dynamicClient:           dynamicInterface,
		discoveryClient:         clientSet.Discovery(),
	}

	// Register handlers
	restores.OnChange(ctx, "restore", controller.OnRestoreChange)
	//backups.OnRemove(ctx, controllerRemoveName, controller.OnEksConfigRemoved)
}

func (h *handler) OnRestoreChange(_ string, restore *v1.Restore) (*v1.Restore, error) {
	fmt.Printf("\nsyncing for restore %v\n", restore.Name)
	created := make(map[string]bool)
	adjacencyList := make(map[*restoreObj][]*restoreObj)
	backupName := restore.Spec.BackupFileName
	backupPath := filepath.Join(util.BackupBaseDir, backupName)
	// if local, backup tar.gz must be added to the "Local" path
	if restore.Spec.Local != "" {
		backupFilePath := filepath.Join(restore.Spec.Local, backupName)
		if err := util.LoadFromTarGzip(backupFilePath); err != nil {
			return restore, err
		}
	} else if restore.Spec.ObjectStore != nil {
		if err := h.downloadFromS3(restore); err != nil {
			return restore, err
		}
		backupFilePath := filepath.Join(util.BackupBaseDir, backupName)
		if err := util.LoadFromTarGzip(backupFilePath); err != nil {
			return restore, err
		}
	}
	backupPath = strings.TrimSuffix(backupPath, ".tar.gz")

	config, err := h.backupEncryptionConfigs.Get(restore.Spec.EncryptionConfigNamespace, restore.Spec.EncryptionConfigName, k8sv1.GetOptions{})
	if err != nil {
		return restore, err
	}
	transformerMap, err := util.GetEncryptionTransformers(config)
	if err != nil {
		return restore, err
	}

	//// first restore CRDs
	if err := h.restoreCRDs(backupPath, "customresourcedefinitions.apiextensions.k8s.io#v1", transformerMap, created); err != nil {
		return restore, fmt.Errorf("error restoring CRD: %v", err)
	}

	// generate adjacency lists for dependents and ownerRefs
	if err := h.generateDependencyGraph(backupPath, transformerMap, adjacencyList); err != nil {
		return restore, err
	}
	fmt.Printf("\nadjacencyList: \n")
	for key, val := range adjacencyList {
		if len(val) > 0 {
			fmt.Printf("dependent: %#v\n", key)
			fmt.Printf("owners:")
			for _, values := range val {
				fmt.Printf("\nvalue: %#v\n", values)
			}
			fmt.Println("")
		}
	}

	if err := h.createFromAdjacencyList(backupPath, adjacencyList, created, transformerMap); err != nil {
		panic(err)
		return restore, err
	}

	return restore, nil

	// now create dependents, for each dependent individual kubectl apply, get owner uid label. Get new obj UID and update
	dependentDirInfo, err := ioutil.ReadDir(backupPath + "/dependents")
	if err != nil {
		return restore, err
	}
	fmt.Printf("Now restoring depedents..")
	var dependentRestoreErr error
	for _, gvDir := range dependentDirInfo {
		gvkStr := gvDir.Name()
		gvkParts := strings.Split(gvkStr, "#")
		version := gvkParts[1]
		kindGrp := strings.SplitN(gvkParts[0], ".", 1)
		kind := kindGrp[0]
		var grp string
		if len(kindGrp) > 1 {
			grp = kindGrp[1]
		}
		fmt.Printf("\nrestoring items of gvk %v, %v, %v\n", grp, version, kind)
		resourceDirPath := filepath.Join(backupPath, "dependents", gvDir.Name())
		resourceDirInfo, err := ioutil.ReadDir(resourceDirPath)
		if err != nil {
			return restore, err
		}
		for _, resourceFile := range resourceDirInfo {
			// read the resource file to get the ownerRef's apiVersion, kind and name.
			resourceFileName := filepath.Join(resourceDirPath, resourceFile.Name())
			originalFileContent, err := ioutil.ReadFile(resourceFileName)
			if err != nil {
				return restore, err
			}
			fileMap := make(map[string]interface{})
			err = json.Unmarshal(originalFileContent, &fileMap)
			if err != nil {
				return restore, err
			}
			metadata := fileMap["metadata"].(map[string]interface{})
			ownerRefs, ok := metadata["ownerReferences"].([]interface{})
			if !ok {
				continue
			}
			for ind, obj := range ownerRefs {
				ownerRef := obj.(map[string]interface{})
				gv, err := schema.ParseGroupVersion(ownerRef["apiVersion"].(string))
				if err != nil {
					return restore, err
				}
				ownerKind := ownerRef["kind"].(string)
				// TODO: proper method for getting plural name from kind
				kind := strings.ToLower(strings.Split(ownerKind, ".")[0]) + "s"
				gvr := gv.WithResource(kind)
				dr := h.dynamicClient.Resource(gvr)
				ownerOldUID := ownerRef["uid"].(string)
				// TODO: which context to use
				ctx := context.Background()
				ownerObj, err := dr.List(ctx, k8sv1.ListOptions{LabelSelector: fmt.Sprintf("%s=%s", util.OldUIDReferenceLabel, ownerOldUID)})
				if err != nil {
					return restore, fmt.Errorf("error listing owner by label: %v", err)
				}
				if len(ownerObj.Items) == 0 {
					logrus.Infof("No %v returned for label %v", kind, fmt.Sprintf("%s=%s", util.OldUIDReferenceLabel, ownerOldUID))
					logrus.Errorf("Owner of %v could be a dependent itself, to try this again, return err at end")
					dependentRestoreErr = fmt.Errorf("retry this")
					continue
				}
				newOwnerUID := ownerObj.Items[0].Object["metadata"].(map[string]interface{})["uid"]
				ownerRef["uid"] = newOwnerUID
				ownerRefs[ind] = ownerRef
			}
			metadata["ownerReferences"] = ownerRefs
			fileMap["metadata"] = metadata
			writeBytes, err := json.Marshal(fileMap)
			if err != nil {
				return restore, fmt.Errorf("error marshaling updated ownerRefs: %v", err)
			}
			err = ioutil.WriteFile(resourceFileName, writeBytes, 0777)
			if err != nil {
				return restore, fmt.Errorf("error writing updated ownerRefs to file: %v", err)
			}
			// apply doubles data set size because of annotation creation; replace the object; use dynaminClient.Update; kubectl replace of create
			output, err := exec.Command("kubectl", "apply", "-f", resourceFileName).CombinedOutput()
			// use dynamic client.Update && client.UpdateStatus for  subsresources
			// use discovery client to find which resource has status subresource
			if err != nil {
				if strings.Contains(string(output), "--validate=false") {
					logrus.Info("Error during restore, retrying with validate=false")
					retryop, err := exec.Command("kubectl", "apply", "-f", resourceFileName, "--validate=false").CombinedOutput()
					if err != nil {
						return restore, fmt.Errorf("error when restoring %v with validate=false: %v", resourceFileName, string(retryop)+err.Error())
					} else {
						logrus.Info("Retry with validate=false succeeded")
						continue
					}
				}
				return restore, err
			}
			err = ioutil.WriteFile(resourceFileName, originalFileContent, 0777)
			if err != nil {
				return restore, fmt.Errorf("error writing original ownerRefs to file: %v", err)
			}
		}
	}
	if dependentRestoreErr != nil {
		return restore, dependentRestoreErr
	}
	if err := h.prune(backupPath, restore.Spec.ForcePruneTimeout); err != nil {
		return restore, fmt.Errorf("error pruning during restore")
	}
	logrus.Infof("Done restoring")
	if err := os.RemoveAll(backupPath); err != nil {
		return restore, err
	}
	return restore, nil
}

func (h *handler) restoreCRDs(backupPath, resourceGVK string, transformerMap map[schema.GroupResource]value.Transformer, created map[string]bool) error {
	resourceDirPath := path.Join(backupPath, resourceGVK)
	gvr := getGVR(resourceGVK)
	gr := gvr.GroupResource()
	//fmt.Printf("\ndr: %v\n", dr)
	decryptionTransformer, _ := transformerMap[gr]
	dirContents, err := ioutil.ReadDir(resourceDirPath)
	if err != nil {
		return err
	}
	for _, resFile := range dirContents {
		resManifestPath := filepath.Join(resourceDirPath, resFile.Name())
		err := h.restoreResource(resManifestPath, resFile.Name(), decryptionTransformer, gvr)
		if err != nil {
			fmt.Printf("\nreturning error %v from here\n", err)
			return fmt.Errorf("restoreCRDs: %v", err)
		}
		restoreObjKey := &restoreObj{
			Name:               strings.TrimSuffix(resFile.Name(), ".json"),
			ResourceConfigPath: resManifestPath,
			GVR:                gvr,
		}
		created[restoreObjKey.ResourceConfigPath] = true
	}
	return nil
}

func (h *handler) generateDependencyGraph(backupPath string, transformerMap map[schema.GroupResource]value.Transformer, adjacencyList map[*restoreObj][]*restoreObj) error {
	backupEntries, err := ioutil.ReadDir(backupPath)
	if err != nil {
		return err
	}

	for _, backupEntry := range backupEntries {
		if !backupEntry.IsDir() {
			// only file is filters.json which we read during prune, so continue
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
			if err := h.addToAdjacencyList(backupPath, resManifestPath, resourceFile.Name(), gvr, transformerMap[gr], adjacencyList); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *handler) addToAdjacencyList(backupPath, resConfigPath, aad string, gvr schema.GroupVersionResource, decryptionTransformer value.Transformer, adjacencyList map[*restoreObj][]*restoreObj) error {
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
	currRestoreObj := &restoreObj{
		Name:               name,
		ResourceConfigPath: resConfigPath,
		GVR:                gvr,
	}
	if isNamespaced {
		currRestoreObj.Namespace = namespace
	}

	var ownersList []*restoreObj
	ownerRefs, ownerRefsFound := metadata[ownerRefsMapKey].([]interface{})
	if !ownerRefsFound {
		adjacencyList[currRestoreObj] = ownersList
		return nil
	}

	for _, owner := range ownerRefs {
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
		// TODO: find alternative other than UnsafeGuessKindToResource
		plural, _ := meta.UnsafeGuessKindToResource(gvk)
		gvr := gv.WithResource(plural.Resource)

		var apiGroup, version string
		split := strings.SplitN(groupVersion, "/", 2)
		if len(split) == 1 {
			version = split[0]
		} else {
			apiGroup = split[0]
			version = split[1]
		}
		// kind + "." + apigroup + "#" + version
		ownerDirPath := fmt.Sprintf("%s.%s#%s", plural.Resource, apiGroup, version)
		ownerName := ownerRefData["name"].(string)
		ownerObj := &restoreObj{
			Name:               ownerName,
			ResourceConfigPath: filepath.Join(backupPath, ownerDirPath, ownerName+".json"),
			GVR:                gvr,
		}

		// if owner is namespaced, it has to be the same ns as the dependent. Add same ns to the owner.
		// is ns?
		mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(h.discoveryClient))
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return err
		}
		if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
			// namespaced resources should specify the namespace
			ownerObj.Namespace = namespace
		}
		//

		ownersList = append(ownersList, ownerObj)
	}
	adjacencyList[currRestoreObj] = ownersList
	return nil
}

func (h *handler) createFromAdjacencyList(backupPath string, adjacencyList map[*restoreObj][]*restoreObj, created map[string]bool, transformerMap map[schema.GroupResource]value.Transformer) error {
	fmt.Printf("\nlen adjacency list: %v\n", len(adjacencyList))
	for len(adjacencyList) > 0 {
		for dependent, owners := range adjacencyList {
			fmt.Printf("\nadjacencyList len: %v\n", len(adjacencyList))
			canCreate := true
			if len(owners) > 0 {
				logrus.Infof("Obj %v has ownerRefs", dependent.Name)
			}
			for _, owner := range owners {
				if !created[owner.ResourceConfigPath] {
					canCreate = false
					break
				}
			}
			if !canCreate {
				logrus.Infof("Cannot create obj %v as its ownerRefs are not created", dependent.Name)
				continue
			}
			// canCreate will be true if obj has no owners, or if all owners are created
			gr := dependent.GVR.GroupResource()
			transformer, _ := transformerMap[gr]
			// iterate all owners to get UID and update file's ownerreferences

			if len(owners) > 0 {
				// open dependent file and update ownerReferences section
				logrus.Infof("Updating ownerRefs for %v since its owners are created", dependent.Name)
				currObjBytes, err := ioutil.ReadFile(dependent.ResourceConfigPath)
				if err != nil {
					return fmt.Errorf("error reading %v: %v", dependent.ResourceConfigPath, err)
				}
				logrus.Infof("Unmarshaling for %v since its owners are created", dependent.Name)
				var currObjContents map[string]interface{}
				if err := json.Unmarshal(currObjBytes, &currObjContents); err != nil {
					return fmt.Errorf("error unmarshaling %v: %v", dependent.ResourceConfigPath, err)
				}

				metadata, _ := currObjContents[metadataMapKey].(map[string]interface{})
				ownerReferences, _ := metadata[ownerRefsMapKey].([]interface{})
				if metadata == nil || ownerReferences == nil {
					// something went wrong, not updating this ownerRef
					fmt.Printf("\nunmarshaled currObjContents: %#v\n", currObjContents)
					logrus.Errorf("Missing ownerRef for %v", dependent.Name)
					continue
				}
				logrus.Infof("looping ownerRefs for %v since its owners are created", dependent.Name)
				for ind, ownerRef := range ownerReferences {
					reference := ownerRef.(map[string]interface{})
					apiversion, _ := reference["apiVersion"].(string)
					kind, _ := reference["kind"].(string)
					if apiversion == "" || kind == "" {
						continue
					}
					logrus.Infof("Parsing gv for ownerRef for %v ", dependent.Name)
					ownerGV, err := schema.ParseGroupVersion(apiversion)
					if err != nil {
						return fmt.Errorf("err %v parsing apiversion %v", err, apiversion)
					}
					ownerGVK := ownerGV.WithKind(kind)
					plural, _ := meta.UnsafeGuessKindToResource(ownerGVK)
					gvr := ownerGV.WithResource(plural.Resource)
					name, _ := reference["name"].(string)
					ownerObj := &restoreObj{
						Name: name,
						GVR:  gvr,
					}
					mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(h.discoveryClient))
					mapping, err := mapper.RESTMapping(ownerGVK.GroupKind(), ownerGVK.Version)
					if err != nil {
						return err
					}
					if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
						// namespaced resources should specify the namespace
						ownerObj.Namespace = dependent.Namespace
					}
					logrus.Infof("Getting new UID for %v ", ownerObj.Name)
					ownerObjNewUID, err := h.getOwnerNewUID(ownerObj)
					if err != nil {
						return fmt.Errorf("error obtaining new UID for %v: %v", ownerObj.Name, err)
					}
					reference["uid"] = ownerObjNewUID
					ownerReferences[ind] = reference
				}
				metadata[ownerRefsMapKey] = ownerReferences
				currObjContents[metadataMapKey] = metadata
				writeBytes, err := json.Marshal(currObjContents)
				if err != nil {
					return fmt.Errorf("error marshaling updated ownerRefs: %v", err)
				}
				err = ioutil.WriteFile(dependent.ResourceConfigPath, writeBytes, 0777)
				if err != nil {
					return fmt.Errorf("error writing updated ownerRefs to file: %v", err)
				}
			}

			if err := h.restoreResource(dependent.ResourceConfigPath, dependent.Name, transformer, dependent.GVR); err != nil {
				return err
			}

			created[dependent.ResourceConfigPath] = true
			delete(adjacencyList, dependent)

		}
	}
	return nil
}

func (h *handler) restoreResource(resConfigPath, aad string, decryptionTransformer value.Transformer, gvr schema.GroupVersionResource) error {
	logrus.Infof("Restoring from %v", resConfigPath)
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
	fmt.Printf("\ndone unmarshaling\n")
	fileMapMetadata := fileMap[metadataMapKey].(map[string]interface{})
	name := fileMapMetadata["name"].(string)
	namespace, _ := fileMapMetadata["namespace"].(string)
	obj := &unstructured.Unstructured{Object: fileMap}
	// TODO: subresources
	var dr dynamic.ResourceInterface
	dr = h.dynamicClient.Resource(gvr)
	if namespace != "" {
		dr = h.dynamicClient.Resource(gvr).Namespace(namespace)
	}
	fmt.Printf("\ngetting %v for %v from ns %v\n", name, gvr, namespace)
	res, err := dr.Get(h.ctx, name, k8sv1.GetOptions{})
	if err != nil {
		fmt.Printf("\nerr when getting resource %w\n", err)
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
