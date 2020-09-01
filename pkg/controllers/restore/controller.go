package restore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	restoreControllers "github.com/rancher/backup-restore-operator/pkg/generated/controllers/resources.cattle.io/v1"
	"github.com/rancher/backup-restore-operator/pkg/util"
	lasso "github.com/rancher/lasso/pkg/client"
	v1core "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/condition"
	"github.com/rancher/wrangler/pkg/genericcondition"
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
	"k8s.io/client-go/util/retry"
)

const (
	metadataMapKey  = "metadata"
	ownerRefsMapKey = "ownerReferences"
	clusterScoped   = "clusterscoped"
	namespaceScoped = "namespaceScoped"
)

type handler struct {
	ctx                     context.Context
	restores                restoreControllers.RestoreController
	backups                 restoreControllers.BackupController
	secrets                 v1core.SecretController
	discoveryClient         discovery.DiscoveryInterface
	dynamicClient           dynamic.Interface
	sharedClientFactory     lasso.SharedClientFactory
	restmapper              meta.RESTMapper
	defaultBackupMountPath  string
	defaultS3BackupLocation *v1.S3ObjectStore
}

type ObjectsFromBackupCR struct {
	crdInfoToData                   map[objInfo]unstructured.Unstructured
	clusterscopedResourceInfoToData map[objInfo]unstructured.Unstructured
	namespacedResourceInfoToData    map[objInfo]unstructured.Unstructured
	resourcesFromBackup             map[string]bool
	resourcesWithStatusSubresource  map[string]bool
	backupResourceSet               v1.ResourceSet
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
	restmapper meta.RESTMapper,
	defaultLocalBackupLocation string,
	defaultS3 *v1.S3ObjectStore) {

	controller := &handler{
		ctx:                     ctx,
		restores:                restores,
		backups:                 backups,
		secrets:                 secrets,
		dynamicClient:           dynamicInterface,
		discoveryClient:         clientSet.Discovery(),
		sharedClientFactory:     sharedClientFactory,
		restmapper:              restmapper,
		defaultBackupMountPath:  defaultLocalBackupLocation,
		defaultS3BackupLocation: defaultS3,
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

	var backupSource string
	backupName := restore.Spec.BackupFilename
	logrus.Infof("Restoring from backup %v", restore.Spec.BackupFilename)
	if restore.Status.NumRetries > 0 {
		logrus.Infof("Retry #%v: Retrying restore from %v", restore.Status.NumRetries, backupName)
	}

	created := make(map[string]bool)
	ownerToDependentsList := make(map[string][]restoreObj)
	var toRestore []restoreObj
	numOwnerReferences := make(map[string]int)
	objFromBackupCR := ObjectsFromBackupCR{
		crdInfoToData:                   make(map[objInfo]unstructured.Unstructured),
		clusterscopedResourceInfoToData: make(map[objInfo]unstructured.Unstructured),
		namespacedResourceInfoToData:    make(map[objInfo]unstructured.Unstructured),
		resourcesFromBackup:             make(map[string]bool),
		resourcesWithStatusSubresource:  make(map[string]bool),
		backupResourceSet:               v1.ResourceSet{},
	}

	transformerMap := make(map[schema.GroupResource]value.Transformer)
	var err error
	if restore.Spec.EncryptionConfigName != "" {
		logrus.Infof("Processing encryption config %v for restore CR %v", restore.Spec.EncryptionConfigName, restore.Name)
		transformerMap, err = util.GetEncryptionTransformers(restore.Spec.EncryptionConfigName, h.secrets)
		if err != nil {
			logrus.Errorf("Error processing encryption config: %v", err)
			return h.setReconcilingCondition(restore, err)
		}
	}

	backupLocation := restore.Spec.StorageLocation
	var foundBackup bool
	if backupLocation == nil {
		if h.defaultS3BackupLocation != nil {
			backupFilePath, err := h.downloadFromS3(restore, h.defaultS3BackupLocation, util.ChartNamespace)
			if err != nil {
				return h.setReconcilingCondition(restore, err)
			}
			if err = h.LoadFromTarGzip(backupFilePath, transformerMap, &objFromBackupCR); err != nil {
				return h.setReconcilingCondition(restore, err)
			}
			// remove the downloaded gzip file from s3
			removeFileErr := os.Remove(backupFilePath)
			if removeFileErr != nil {
				return restore, removeFileErr
			}
			foundBackup = true
			backupSource = util.S3Backup
		} else if h.defaultBackupMountPath != "" {
			backupFilePath := filepath.Join(h.defaultBackupMountPath, backupName)
			if err = h.LoadFromTarGzip(backupFilePath, transformerMap, &objFromBackupCR); err != nil {
				return h.setReconcilingCondition(restore, err)
			}
			foundBackup = true
			backupSource = util.PVCBackup
		}
	} else if backupLocation.S3 != nil {
		backupFilePath, err := h.downloadFromS3(restore, restore.Spec.StorageLocation.S3, restore.Namespace)
		if err != nil {
			return h.setReconcilingCondition(restore, err)
		}
		if err = h.LoadFromTarGzip(backupFilePath, transformerMap, &objFromBackupCR); err != nil {
			return h.setReconcilingCondition(restore, err)
		}
		// remove the downloaded gzip file from s3
		removeFileErr := os.Remove(backupFilePath)
		if removeFileErr != nil {
			return restore, removeFileErr
		}
		foundBackup = true
		backupSource = util.S3Backup
	}
	if !foundBackup {
		return h.setReconcilingCondition(restore, fmt.Errorf("Backup location not specified on the restore CR, and not configured at the operator level"))
	}

	// first stop the controllers
	h.scaleDownControllersFromResourceSet(objFromBackupCR)

	// first restore CRDs
	logrus.Infof("Starting to restore CRDs for restore CR %v", restore.Name)
	if err := h.restoreCRDs(created, objFromBackupCR); err != nil {
		h.scaleUpControllersFromResourceSet(objFromBackupCR)
		return h.setReconcilingCondition(restore, err)
	}

	logrus.Infof("Starting to restore clusterscoped resources for restore CR %v", restore.Name)
	// then restore clusterscoped resources, by first generating dependency graph for cluster scoped resources, and create from the graph
	if err := h.restoreClusterScopedResources(ownerToDependentsList, &toRestore, numOwnerReferences, created, objFromBackupCR); err != nil {
		h.scaleUpControllersFromResourceSet(objFromBackupCR)
		return h.setReconcilingCondition(restore, err)
	}

	logrus.Infof("Starting to restore namespaced resources for restore CR %v", restore.Name)
	// now restore namespaced resources: generate adjacency lists for dependents and ownerRefs for namespaced resources
	ownerToDependentsList = make(map[string][]restoreObj)
	toRestore = []restoreObj{}
	if err := h.restoreNamespacedResources(ownerToDependentsList, &toRestore, numOwnerReferences, created, objFromBackupCR); err != nil {
		h.scaleUpControllersFromResourceSet(objFromBackupCR)
		return h.setReconcilingCondition(restore, err)
	}

	// prune by default
	if restore.Spec.Prune == nil || *restore.Spec.Prune == true {
		logrus.Infof("Pruning resources that are not part of the backup for restore CR %v", restore.Name)
		if err := h.prune(objFromBackupCR.backupResourceSet.ResourceSelectors, transformerMap, objFromBackupCR, restore.Spec.DeleteTimeoutSeconds); err != nil {
			h.scaleUpControllersFromResourceSet(objFromBackupCR)
			return h.setReconcilingCondition(restore, fmt.Errorf("error pruning during restore: %v", err))
		}
	}
	h.scaleUpControllersFromResourceSet(objFromBackupCR)

	updateErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		restore, err = h.restores.Get(restore.Namespace, restore.Name, k8sv1.GetOptions{})
		if err != nil {
			return err
		}

		// reset conditions to remove the reconciling condition, because as per kstatus lib its presence is considered an error
		restore.Status.Conditions = []genericcondition.GenericCondition{}
		condition.Cond(v1.RestoreConditionReady).SetStatusBool(restore, true)
		condition.Cond(v1.RestoreConditionReady).Message(restore, "Completed")

		restore.Status.RestoreCompletionTS = time.Now().Format(time.RFC3339)
		restore.Status.ObservedGeneration = restore.Generation
		restore.Status.BackupSource = backupSource
		_, err = h.restores.UpdateStatus(restore)
		return err
	})
	if updateErr != nil {
		return h.setReconcilingCondition(restore, updateErr)
	}
	logrus.Infof("Done restoring")
	return restore, err
}

func (h *handler) restoreCRDs(created map[string]bool, objFromBackupCR ObjectsFromBackupCR) error {
	// Both CRD apiversions have different way of indicating presence of status subresource
	for crdInfo, crdData := range objFromBackupCR.crdInfoToData {
		err := h.restoreResource(crdInfo, crdData, false)
		if err != nil {
			return fmt.Errorf("restoreCRDs: %v", err)
		}
		created[crdInfo.ConfigPath] = true
	}
	return nil
}

func (h *handler) restoreClusterScopedResources(ownerToDependentsList map[string][]restoreObj, toRestore *[]restoreObj,
	numOwnerReferences map[string]int, created map[string]bool, objFromBackupCR ObjectsFromBackupCR) error {
	// generate adjacency lists for dependents and ownerRefs first for clusterscoped resources
	if err := h.generateDependencyGraph(ownerToDependentsList, toRestore, numOwnerReferences, objFromBackupCR, clusterScoped); err != nil {
		return err
	}
	return h.createFromDependencyGraph(ownerToDependentsList, created, numOwnerReferences, objFromBackupCR, *toRestore)
}

func (h *handler) restoreNamespacedResources(ownerToDependentsList map[string][]restoreObj, toRestore *[]restoreObj,
	numOwnerReferences map[string]int, created map[string]bool, objFromBackupCR ObjectsFromBackupCR) error {
	// generate adjacency lists for dependents and ownerRefs first for clusterscoped resources
	if err := h.generateDependencyGraph(ownerToDependentsList, toRestore, numOwnerReferences, objFromBackupCR, namespaceScoped); err != nil {
		return err
	}
	return h.createFromDependencyGraph(ownerToDependentsList, created, numOwnerReferences, objFromBackupCR, *toRestore)
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
	numOwnerReferences map[string]int, objFromBackupCR ObjectsFromBackupCR, scope string) error {
	var resourceInfoToData map[objInfo]unstructured.Unstructured
	switch scope {
	case clusterScoped:
		resourceInfoToData = objFromBackupCR.clusterscopedResourceInfoToData
	case namespaceScoped:
		resourceInfoToData = objFromBackupCR.namespacedResourceInfoToData
	}
	for resourceInfo, resourceData := range resourceInfoToData {
		// add to adjacency list
		name := resourceInfo.Name
		namespace := resourceInfo.Namespace
		gvr := resourceInfo.GVR
		// TODO: Maybe restoreObj won't be needed
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
		logrus.Infof("Checking ownerRefs for resource %v of type %v", name, gvr.String())
		for _, owner := range ownerRefs {
			numOwners++
			ownerRefData, ok := owner.(map[string]interface{})
			if !ok {
				logrus.Errorf("Invalid ownerRef for resource %v of type %v", name, gvr.String())
				continue
			}

			groupVersion := ownerRefData["apiVersion"].(string)
			gv, err := schema.ParseGroupVersion(groupVersion)
			if err != nil {
				logrus.Errorf("Error parsing ownerRef apiVersion %v for resource %v: %v", groupVersion, name, err)
				continue
			}
			kind := ownerRefData["kind"].(string)
			gvk := gv.WithKind(kind)
			logrus.Infof("Getting GVR for ownerRef %v of resource %v", gvk.String(), name)
			ownerGVR, isNamespaced, err := h.sharedClientFactory.ResourceForGVK(gvk)
			if err != nil {
				// Prior to Rancher 2.4.5, following resources had roles&rolebindings with malformed ownerRefs:
				// Secrets for cloud creds; NodeTemplates; ClusterTemplates & Revisions; Multiclusterapps & GlobalDNS
				// Kind was replaced by the resource name in plural and APIVersion field only contained the group and not version
				// Error is of the kind:  Kind=nodetemplates: no matches for kind "nodetemplates" in version "management.cattle.io"
				// this is an invalid ownerRef, can't restore current resource with this ownerRef. But if we continue and this resource has no valid ownerRef it won't get restored
				// so decrement numOwners. if the curr object has at least one valid ownerRef, it will get added to ownersToDependents list
				// but for objects like the rancher 2.4.5, check at the end of this loop if even a single ownerRef is found, if not add it to toRestore
				numOwners--
				logrus.Errorf("Invalid ownerRef %v, either of the fields is incorrect: APIVersion or Kind", gvk.String())
				logrus.Errorf("Error getting ownerRef %v for object %v(of %v): %v", gvk.String(), name, gvr.String(), err)
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
		if numOwners > 0 {
			numOwnerReferences[currRestoreObj.ResourceConfigPath] = numOwners
		} else {
			// Errors were encountered while processing ownerRefs for this object, so it should get restored without any ownerRefs,
			// add it to toRestore
			logrus.Warnf("Resource %v of type %v has invalid ownerRefs, adding it to restore queue by dropping the ownerRefs", name, gvr.String())
			delete(currRestoreObj.Data.Object[metadataMapKey].(map[string]interface{}), ownerRefsMapKey)
			*toRestore = append(*toRestore, currRestoreObj)
		}
	}
	return nil
}

func (h *handler) createFromDependencyGraph(ownerToDependentsList map[string][]restoreObj, created map[string]bool,
	numOwnerReferences map[string]int, objFromBackupCR ObjectsFromBackupCR, toRestore []restoreObj) error {
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
			logrus.Infof("Resource %v is already created/updated", curr.ResourceConfigPath)
			continue
		}
		currResourceInfo := objInfo{
			Name:       curr.Name,
			Namespace:  curr.Namespace,
			GVR:        curr.GVR,
			ConfigPath: curr.ResourceConfigPath,
		}
		var resourceData unstructured.Unstructured
		if curr.Namespace != "" {
			resourceData = objFromBackupCR.namespacedResourceInfoToData[currResourceInfo]
		} else {
			resourceData = objFromBackupCR.clusterscopedResourceInfoToData[currResourceInfo]
		}
		if err := h.restoreResource(currResourceInfo, resourceData, objFromBackupCR.resourcesWithStatusSubresource[curr.GVR.String()]); err != nil {
			logrus.Errorf("Error restoring resource %v of type %v", currResourceInfo.Name, currResourceInfo.GVR)
			errList = append(errList, fmt.Errorf("error restoring %v of type %v: %v", currResourceInfo.Name, currResourceInfo.GVR, err))
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
		if err := h.updateOwnerRefs(ownerReferences, namespace); err != nil {
			if apierrors.IsNotFound(err) {
				// This can only happen when the ownerRefs are created in a way that violates k8s design https://kubernetes.io/docs/concepts/workloads/controllers/garbage-collection/
				// Although disallowed, k8s currently has a bug where it allows creating cross-namespaced ownerRefs, and lets create clusterscoped objects with namespaced owners
				// https://github.com/kubernetes/kubernetes/issues/65200
				logrus.Warnf("Could not find ownerRef for resource %v", name)
				// if owner not found, still restore resource but drop the ownerRefs field,
				// because k8s terminates objects with invalid ownerRef UIDs
				delete(obj.Object[metadataMapKey].(map[string]interface{}), ownerRefsMapKey)
				logrus.Warnf("Resource %v will be restored without ownerReferences, edit it to add required ownerReferences", name)
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

	logrus.Infof("Successfully restored %v", name)
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
	time.Sleep(2 * time.Second)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var err error
		updRestore, err := h.restores.Get(restore.Namespace, restore.Name, k8sv1.GetOptions{})
		if err != nil {
			return err
		}

		updRestore.Status.NumRetries++
		condition.Cond(v1.RestoreConditionReconciling).SetStatusBool(updRestore, true)
		condition.Cond(v1.RestoreConditionReconciling).SetError(updRestore, "", originalErr)
		condition.Cond(v1.BackupConditionReady).Message(updRestore, "Retrying")

		_, err = h.restores.UpdateStatus(updRestore)
		return err
	})
	if err != nil {
		return restore, errors.New(originalErr.Error() + err.Error())
	}

	return restore, err
}
