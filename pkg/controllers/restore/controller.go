package restore

import (
	"context"
	"errors"
	"fmt"
	"github.com/rancher/wrangler/pkg/condition"
	"os"
	"path/filepath"
	"strings"
	"time"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	restoreControllers "github.com/rancher/backup-restore-operator/pkg/generated/controllers/resources.cattle.io/v1"
	"github.com/rancher/backup-restore-operator/pkg/util"
	lasso "github.com/rancher/lasso/pkg/client"
	v1core "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/sirupsen/logrus"

	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
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
	clusterScoped   = "clusterscoped"
	namespaceScoped = "namespaceScoped"
)

type handler struct {
	ctx                             context.Context
	restores                        restoreControllers.RestoreController
	backups                         restoreControllers.BackupController
	secrets                         v1core.SecretController
	discoveryClient                 discovery.DiscoveryInterface
	dynamicClient                   dynamic.Interface
	sharedClientFactory             lasso.SharedClientFactory
	restmapper                      meta.RESTMapper
	crdInfoToData                   map[objInfo]unstructured.Unstructured
	clusterscopedResourceInfoToData map[objInfo]unstructured.Unstructured
	namespacedResourceInfoToData    map[objInfo]unstructured.Unstructured
	resourcesFromBackup             map[string]bool
	resourcesWithStatusSubresource  map[string]bool
}

type objInfo struct {
	Name       string
	Namespace  string
	GVR        schema.GroupVersionResource
	ConfigPath string
}

type restoreObj struct {
	Name               string
	Namespace          string
	GVR                schema.GroupVersionResource
	ResourceConfigPath string
	Data               *unstructured.Unstructured
}

func Register(
	ctx context.Context,
	restores restoreControllers.RestoreController,
	backups restoreControllers.BackupController,
	secrets v1core.SecretController,
	clientSet *clientset.Clientset,
	dynamicInterface dynamic.Interface,
	sharedClientFactory lasso.SharedClientFactory,
	restmapper meta.RESTMapper) {

	controller := &handler{
		ctx:                 ctx,
		restores:            restores,
		backups:             backups,
		secrets:             secrets,
		dynamicClient:       dynamicInterface,
		discoveryClient:     clientSet.Discovery(),
		sharedClientFactory: sharedClientFactory,
		restmapper:          restmapper,
	}

	// Register handlers
	restores.OnChange(ctx, "restore", controller.OnRestoreChange)
}

func (h *handler) OnRestoreChange(_ string, restore *v1.Restore) (*v1.Restore, error) {
	if restore == nil || restore.DeletionTimestamp != nil {
		return restore, nil
	}
	if restore.Status.RestoreCompletionTS != "" {
		return restore, nil
	}
	created := make(map[string]bool)
	ownerToDependentsList := make(map[string][]restoreObj)
	var toRestore []restoreObj
	numOwnerReferences := make(map[string]int)
	h.resourcesWithStatusSubresource = make(map[string]bool)
	h.crdInfoToData = make(map[objInfo]unstructured.Unstructured)
	h.clusterscopedResourceInfoToData = make(map[objInfo]unstructured.Unstructured)
	h.namespacedResourceInfoToData = make(map[objInfo]unstructured.Unstructured)
	h.resourcesFromBackup = make(map[string]bool)

	backupName := restore.Spec.BackupFilename

	backupLocation := restore.Spec.StorageLocation
	if backupLocation == nil {
		return h.setReconcilingCondition(restore, fmt.Errorf("specify backup location during restore"))
	}
	transformerMap := make(map[schema.GroupResource]value.Transformer)
	var err error
	if restore.Spec.EncryptionConfigName != "" {
		transformerMap, err = util.GetEncryptionTransformers(restore.Spec.EncryptionConfigName, h.secrets)
		if err != nil {
			return h.setReconcilingCondition(restore, err)
		}
	}

	var resourceSelectors []v1.ResourceSelector
	if backupLocation.Local != "" {
		// if local, backup tar.gz must be added to the "Local" path
		backupFilePath := filepath.Join(backupLocation.Local, backupName)
		if resourceSelectors, err = h.LoadFromTarGzip(backupFilePath, transformerMap); err != nil {
			return h.setReconcilingCondition(restore, err)
		}
		fmt.Printf("clusterscopedResourceInfoToData len: %v\n", len(h.clusterscopedResourceInfoToData))
	} else if backupLocation.S3 != nil {
		backupFilePath, err := h.downloadFromS3(restore)
		if err != nil {
			return h.setReconcilingCondition(restore, err)
		}
		if resourceSelectors, err = h.LoadFromTarGzip(backupFilePath, transformerMap); err != nil {
			return h.setReconcilingCondition(restore, err)
		}
		// remove the downloaded gzip file from s3
		removeFileErr := os.Remove(backupFilePath)
		if removeFileErr != nil {
			return restore, removeFileErr
		}
	}

	// first restore CRDs
	startTimeCRDs := time.Now()
	logrus.Infof("Starting to restore CRDs at %v", startTimeCRDs)
	if err := h.restoreCRDs(created); err != nil {
		return h.setReconcilingCondition(restore, err)
	}
	timeForRestoringCRDs := time.Since(startTimeCRDs)
	doneRestoringCRDstime := time.Now()
	logrus.Infof("Time taken to restore CRDs: %v", timeForRestoringCRDs)

	// generate adjacency lists for dependents and ownerRefs first for clusterscoped resources
	if err := h.generateDependencyGraph(ownerToDependentsList, &toRestore, numOwnerReferences, clusterScoped); err != nil {
		return h.setReconcilingCondition(restore, err)
	}
	timeForGeneratingCSGraph := time.Since(doneRestoringCRDstime)
	logrus.Infof("Time taken to generate graph for clusterscoped resources: %v", timeForGeneratingCSGraph)

	doneGeneratingCSGraphTime := time.Now()
	logrus.Infof("Starting to restore clusterscoped resources at %v", doneGeneratingCSGraphTime)
	if err := h.createFromDependencyGraph(ownerToDependentsList, created, numOwnerReferences, toRestore); err != nil {
		return h.setReconcilingCondition(restore, err)
	}
	timeForRestoringResources := time.Since(doneGeneratingCSGraphTime)
	logrus.Infof("Time taken to restore clusterscoped resources: %v\n", timeForRestoringResources)

	// now generate adjacency lists for dependents and ownerRefs for namespaced resources
	ownerToDependentsList = make(map[string][]restoreObj)
	toRestore = []restoreObj{}
	doneRestoringNamespacedTime := time.Now()
	// generate adjacency lists for dependents and ownerRefs
	if err := h.generateDependencyGraph(ownerToDependentsList, &toRestore, numOwnerReferences, namespaceScoped); err != nil {
		return h.setReconcilingCondition(restore, err)
	}
	timeForGeneratingNamespacedGraph := time.Since(doneRestoringNamespacedTime)
	logrus.Infof("Time taken to generate graph for namespace scoped: %v", timeForGeneratingNamespacedGraph)

	doneGeneratingNamespacedGraphTime := time.Now()
	logrus.Infof("Starting to restore namespaced resources at %v", doneGeneratingNamespacedGraphTime)
	if err := h.createFromDependencyGraph(ownerToDependentsList, created, numOwnerReferences, toRestore); err != nil {
		return h.setReconcilingCondition(restore, err)
	}
	timeForRestoringNamespacedResources := time.Since(doneGeneratingNamespacedGraphTime)
	logrus.Infof("Time taken to restore namespaced resources: %v", timeForRestoringNamespacedResources)

	// prune by default
	if restore.Spec.Prune == nil || *restore.Spec.Prune == true {
		if err := h.prune(resourceSelectors, transformerMap, restore.Spec.DeleteTimeout); err != nil {
			return h.setReconcilingCondition(restore, fmt.Errorf("error pruning during restore: %v", err))
		}
	}
	restore.Status.RestoreCompletionTS = time.Now().Format(time.RFC3339)
	condition.Cond(v1.RestoreConditionReady).SetStatusBool(restore, true)
	restore.Status.ObservedGeneration = restore.Generation
	_, err = h.restores.UpdateStatus(restore)
	logrus.Infof("Done restoring")
	return restore, err
}

func (h *handler) restoreCRDs(created map[string]bool) error {
	// Both CRD apiversions have different way of indicating presence of status subresource
	for crdInfo, crdData := range h.crdInfoToData {
		err := h.restoreResource(crdInfo, crdData, false)
		if err != nil {
			return fmt.Errorf("restoreCRDs: %v", err)
		}
		created[crdInfo.ConfigPath] = true
	}
	return nil
}

// generateDependencyGraph creates a graph "ownerToDependentsList" to track objects with ownerReferences
// any "node" in this graph is a map entry, where key = owning object, value = list of its dependents
// all objects that do not have ownerRefs are added to the "toRestore" list
// numOwnerReferences keeps track of how many owners any object has that haven't been restored yet
/* if the file has ownerRefences:
1. it iterates over each ownerRef,
2. creates an entry for each owner in ownerToDependentsList", with the current object in the value list
3. gets total count of ownerRefs and adds current object to "numOwnerReferences" map to indicate the count*/
func (h *handler) generateDependencyGraph(ownerToDependentsList map[string][]restoreObj, toRestore *[]restoreObj,
	numOwnerReferences map[string]int, scope string) error {
	var resourceInfoToData map[objInfo]unstructured.Unstructured
	switch scope {
	case clusterScoped:
		resourceInfoToData = h.clusterscopedResourceInfoToData
	case namespaceScoped:
		resourceInfoToData = h.namespacedResourceInfoToData
	}
	for resourceInfo, resourceData := range resourceInfoToData {
		// add to adjacency list
		name := resourceInfo.Name
		namespace := resourceInfo.Namespace
		gvr := resourceInfo.GVR
		currRestoreObj := restoreObj{
			Name:               name,
			Namespace:          namespace,
			ResourceConfigPath: resourceInfo.ConfigPath,
			GVR:                gvr,
			Data:               &resourceData,
		}

		metadata := resourceData.Object[metadataMapKey].(map[string]interface{})
		ownerRefs, ownerRefsFound := metadata[ownerRefsMapKey].([]interface{})
		if !ownerRefsFound {
			// has no dependents, so no need to add to adjacency list, add to restoreResources list
			*toRestore = append(*toRestore, currRestoreObj)
			continue
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
				ResourceConfigPath: filepath.Join(ownerDirPath, ownerName+".json"),
				GVR:                ownerGVR,
			}
			if isNamespaced {
				// if owning object is namespaced, then it has to be the same ns as the current dependent object
				ownerObj.Namespace = currRestoreObj.Namespace
				// the owner object's resourceFile in backup would also have namespace in the filename, so update
				// ownerObj.ResourceConfigPath to include namespace subdir before the filename for owner
				ownerFilename := filepath.Join(currRestoreObj.Namespace, ownerName+".json")
				ownerObj.ResourceConfigPath = filepath.Join(ownerDirPath, ownerFilename)
			}
			ownerObjDependents, ok := ownerToDependentsList[ownerObj.ResourceConfigPath]
			if !ok {
				ownerToDependentsList[ownerObj.ResourceConfigPath] = []restoreObj{currRestoreObj}
			} else {
				ownerToDependentsList[ownerObj.ResourceConfigPath] = append(ownerObjDependents, currRestoreObj)
			}
		}
		numOwnerReferences[currRestoreObj.ResourceConfigPath] = numOwners
	}
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
		// TODO add resourcename to error to print summary
		// TODO if owner not found, it has to be cross-namespaced dependency, so still create this obj: log this
		// log if you're dropping ownerRefs
		currResourceInfo := objInfo{
			Name:       curr.Name,
			Namespace:  curr.Namespace,
			GVR:        curr.GVR,
			ConfigPath: curr.ResourceConfigPath,
		}
		var resourceData unstructured.Unstructured
		if curr.Namespace != "" {
			resourceData = h.namespacedResourceInfoToData[currResourceInfo]
		} else {
			resourceData = h.clusterscopedResourceInfoToData[currResourceInfo]
		}
		if err := h.restoreResource(currResourceInfo, resourceData, h.resourcesWithStatusSubresource[curr.GVR.String()]); err != nil {
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
	// TODO: LOG all skipped objects with reasons
	return util.ErrList(errList)
}

func (h *handler) restoreResource(restoreObjInfo objInfo, restoreObjData unstructured.Unstructured, hasStatusSubresource bool) error {
	logrus.Infof("restoreResource: Restoring %v of type %v", restoreObjInfo.Name, restoreObjInfo.GVR)

	fileMap := restoreObjData.Object
	obj := restoreObjData

	fileMapMetadata := fileMap[metadataMapKey].(map[string]interface{})
	name := restoreObjInfo.Name
	namespace := restoreObjInfo.Namespace
	gvr := restoreObjInfo.GVR
	var dr dynamic.ResourceInterface
	dr = h.dynamicClient.Resource(gvr)
	if namespace != "" {
		dr = h.dynamicClient.Resource(gvr).Namespace(namespace)
	}
	ownerReferences, _ := fileMapMetadata[ownerRefsMapKey].([]interface{})
	if ownerReferences != nil {
		// no-cross ns, restoreA: error, network
		if err := h.updateOwnerRefs(ownerReferences, namespace); err != nil {
			if apierrors.IsNotFound(err) {
				// if owner not found, still restore resource but drop the ownerRefs field,
				// because k8s terminates objects with invalid ownerRef UIDs
				delete(obj.Object[metadataMapKey].(map[string]interface{}), ownerRefsMapKey)
				// user.ownerRef = catalogTemplate
			} else {
				return err
			}
		}
	}

	res, err := dr.Get(h.ctx, name, k8sv1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("restoreResource: err getting resource %v", err)
		}
		// create and return
		createdObj, err := dr.Create(h.ctx, &obj, k8sv1.CreateOptions{})
		if err != nil {
			return err
		}
		if hasStatusSubresource {
			logrus.Infof("Updating status subresource for %#v", name)
			_, err := dr.UpdateStatus(h.ctx, createdObj, k8sv1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("restoreResource: err updating status resource %v", err)
			}
		}
		return nil
	}
	resMetadata := res.Object[metadataMapKey].(map[string]interface{})
	resourceVersion := resMetadata["resourceVersion"].(string)
	obj.Object[metadataMapKey].(map[string]interface{})["resourceVersion"] = resourceVersion
	_, err = dr.Update(h.ctx, &obj, k8sv1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("restoreResource: err updating resource %v", err)
	}
	if hasStatusSubresource {
		logrus.Infof("Updating status subresource for %#v", name)
		_, err := dr.UpdateStatus(h.ctx, &obj, k8sv1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("restoreResource: err updating status resource %v", err)
		}
	}

	fmt.Printf("\nSuccessfully restored %v\n", name)
	return nil
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
		// ns.OwnerRef = cluster
		// namespace can only be owned by cluster-scoped objects, SO
		// CRDs, cluster-scoped, then namespaced
		// obj in ns A has owner ref to obj in ns B: what t
		// ns.ownerRefs
		// https://github.com/kubernetes/kubernetes/issues/65200
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
			// not found error should be handled separately
			if apierrors.IsNotFound(err) {
				return err
			}
			// obj in ns A has owner ref to obj in ns B: check what err is, mostly not found
			return fmt.Errorf("error obtaining new UID for %v: %v", ownerObj.Name, err)
		}
		reference["uid"] = ownerObjNewUID
		ownerReferences[ind] = reference
	}
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

// getGVR parses the directory path to provide groupVersionResource
func getGVR(resourceGVR string) schema.GroupVersionResource {
	gvkParts := strings.Split(resourceGVR, "#")
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

// https://github.com/kubernetes-sigs/cli-utils/tree/master/pkg/kstatus
// Reconciling and Stalled conditions are present and with a value of true whenever something unusual happens.
func (h *handler) setReconcilingCondition(restore *v1.Restore, originalErr error) (*v1.Restore, error) {
	condition.Cond(v1.RestoreConditionReconciling).SetStatusBool(restore, true)
	if updRestore, err := h.restores.UpdateStatus(restore); err != nil {
		return updRestore, errors.New(originalErr.Error() + err.Error())
	}
	return restore, originalErr
}
