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
	"github.com/rancher/wrangler/pkg/slice"
	"github.com/sirupsen/logrus"

	coordinationv1 "k8s.io/api/coordination/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/storage/value"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	coordinationclientv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/pointer"
)

const (
	metadataMapKey     = "metadata"
	ownerRefsMapKey    = "ownerReferences"
	clusterScoped      = "clusterscoped"
	namespaceScoped    = "namespaceScoped"
	leaseName          = "restore-controller"
	specMapKey         = "spec"
	subResourcesMapKey = "subresources"
	versionMapKey      = "versions"
)

type handler struct {
	ctx                     context.Context
	restores                restoreControllers.RestoreController
	backups                 restoreControllers.BackupController
	secrets                 v1core.SecretController
	discoveryClient         discovery.DiscoveryInterface
	apiClient               clientset.Interface
	dynamicClient           dynamic.Interface
	sharedClientFactory     lasso.SharedClientFactory
	restmapper              meta.RESTMapper
	defaultBackupMountPath  string
	defaultS3BackupLocation *v1.S3ObjectStore
	kubernetesLeaseClient   coordinationclientv1.LeaseInterface
}

type ObjectsFromBackupCR struct {
	crdInfoToData                   map[objInfo]unstructured.Unstructured
	clusterscopedResourceInfoToData map[objInfo]unstructured.Unstructured
	namespacedResourceInfoToData    map[objInfo]unstructured.Unstructured
	resourcesFromBackup             map[string]bool
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
	leaseClient coordinationclientv1.LeaseInterface,
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
		apiClient:               clientSet,
		sharedClientFactory:     sharedClientFactory,
		restmapper:              restmapper,
		defaultBackupMountPath:  defaultLocalBackupLocation,
		defaultS3BackupLocation: defaultS3,
		kubernetesLeaseClient:   leaseClient,
	}

	lease, err := leaseClient.Get(ctx, leaseName, k8sv1.GetOptions{})
	if err == nil && lease != nil {
		leaseClient.Delete(ctx, leaseName, k8sv1.DeleteOptions{})
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

	if err := h.Lock(restore); err != nil {
		return restore, err
	}
	defer h.Unlock(*leaseHolderName(restore))

	logrus.Infof("Processing Restore CR %v", restore.Name)
	var backupSource string
	backupName := restore.Spec.BackupFilename
	logrus.Infof("Restoring from backup %v", restore.Spec.BackupFilename)

	created := make(map[string]bool)
	ownerToDependentsList := make(map[string][]restoreObj)
	var crdsWithSubStatus []string
	var toRestore []restoreObj
	numOwnerReferences := make(map[string]int)
	objFromBackupCR := ObjectsFromBackupCR{
		crdInfoToData:                   make(map[objInfo]unstructured.Unstructured),
		clusterscopedResourceInfoToData: make(map[objInfo]unstructured.Unstructured),
		namespacedResourceInfoToData:    make(map[objInfo]unstructured.Unstructured),
		resourcesFromBackup:             make(map[string]bool),
		backupResourceSet:               v1.ResourceSet{},
	}

	transformerMap := make(map[schema.GroupResource]value.Transformer)
	var err error
	if restore.Spec.EncryptionConfigSecretName != "" {
		logrus.Infof("Processing encryption config %v for restore CR %v", restore.Spec.EncryptionConfigSecretName, restore.Name)
		transformerMap, err = util.GetEncryptionTransformers(restore.Spec.EncryptionConfigSecretName, h.secrets)
		if err != nil {
			logrus.Errorf("Error processing encryption config: %v", err)
			return h.setReconcilingCondition(restore, err)
		}
	}

	backupLocation := restore.Spec.StorageLocation
	var foundBackup bool
	if backupLocation == nil {
		if h.defaultS3BackupLocation != nil {
			backupFilePath, err := h.downloadFromS3(restore, h.defaultS3BackupLocation)
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
			backupSource = util.PVBackup
		}
	} else if backupLocation.S3 != nil {
		backupFilePath, err := h.downloadFromS3(restore, restore.Spec.StorageLocation.S3)
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
	if crdsWithSubStatus, err = h.restoreCRDs(created, objFromBackupCR); err != nil {
		h.scaleUpControllersFromResourceSet(objFromBackupCR)
		logrus.Errorf("Error restoring CRDs %v", err)
		// Cannot set the exact error on reconcile condition, the order in which resources failed to restore are added in err msg could
		// change with each restore, which means the condition will get updated on each try
		return h.setReconcilingCondition(restore, fmt.Errorf("error restoring CRDs, check logs for exact error"))
	}

	logrus.Infof("Starting to restore clusterscoped resources for restore CR %v", restore.Name)
	// then restore clusterscoped resources, by first generating dependency graph for cluster scoped resources, and create from the graph
	if err := h.restoreClusterScopedResources(ownerToDependentsList, &toRestore, numOwnerReferences, created, objFromBackupCR, crdsWithSubStatus); err != nil {
		h.scaleUpControllersFromResourceSet(objFromBackupCR)
		logrus.Errorf("Error restoring cluster-scoped resources %v", err)
		return h.setReconcilingCondition(restore, fmt.Errorf("error restoring cluster-scoped resources, check logs for exact error"))
	}

	logrus.Infof("Starting to restore namespaced resources for restore CR %v", restore.Name)
	// now restore namespaced resources: generate adjacency lists for dependents and ownerRefs for namespaced resources
	ownerToDependentsList = make(map[string][]restoreObj)
	toRestore = []restoreObj{}
	if err := h.restoreNamespacedResources(ownerToDependentsList, &toRestore, numOwnerReferences, created, objFromBackupCR, crdsWithSubStatus); err != nil {
		h.scaleUpControllersFromResourceSet(objFromBackupCR)
		logrus.Errorf("Error restoring namespaced resources %v", err)
		return h.setReconcilingCondition(restore, fmt.Errorf("error restoring namespaced resources, check logs for exact error"))
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
		restore, err = h.restores.Get(restore.Name, k8sv1.GetOptions{})
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

func (h *handler) restoreCRDs(created map[string]bool, objFromBackupCR ObjectsFromBackupCR) (crdsWithStatus []string, err error) {
	for crdInfo, crdData := range objFromBackupCR.crdInfoToData {
		err := h.restoreResource(crdInfo, crdData, false)
		if err != nil {
			return crdsWithStatus, fmt.Errorf("restoreCRDs: %v", err)
		}
		created[crdInfo.ConfigPath] = true
		crds := getCRDsWithSubresourceStatus(crdData)
		if len(crds) > 0 {
			logrus.Debugf("Adding the following to the list of CRDs with the subresource Status: %v", crds)
			crdsWithStatus = append(crdsWithStatus, crds...)
		}
	}
	for crdInfo := range objFromBackupCR.crdInfoToData {
		if err := h.waitCRD(crdInfo.Name); err != nil {
			return crdsWithStatus, err
		}
	}
	return crdsWithStatus, nil
}

func (h *handler) waitCRD(crdName string) error {
	logrus.Infof("Waiting for CRD %s to become available", crdName)
	defer logrus.Infof("Done waiting for CRD %s to become available", crdName)

	first := true
	return wait.Poll(500*time.Millisecond, 60*time.Second, func() (bool, error) {
		if !first {
			logrus.Infof("Waiting for CRD %s to become available", crdName)
		}
		first = false

		crd, err := h.apiClient.ApiextensionsV1beta1().CustomResourceDefinitions().Get(h.ctx, crdName, k8sv1.GetOptions{})
		if err != nil {
			return false, err
		}
		for _, cond := range crd.Status.Conditions {
			switch cond.Type {
			case apiext.Established:
				if cond.Status == apiext.ConditionTrue {
					return true, err
				}
			case apiext.NamesAccepted:
				if cond.Status == apiext.ConditionFalse {
					logrus.Infof("Name conflict on %s: %v\n", crdName, cond.Reason)
				}
			}
		}
		return false, h.ctx.Err()
	})
}

func (h *handler) restoreClusterScopedResources(ownerToDependentsList map[string][]restoreObj, toRestore *[]restoreObj,
	numOwnerReferences map[string]int, created map[string]bool, objFromBackupCR ObjectsFromBackupCR, crdsWithSubStatus []string) error {
	// generate adjacency lists for dependents and ownerRefs first for clusterscoped resources
	if err := h.generateDependencyGraph(ownerToDependentsList, toRestore, numOwnerReferences, objFromBackupCR, created, clusterScoped); err != nil {
		return err
	}
	return h.createFromDependencyGraph(ownerToDependentsList, created, numOwnerReferences, objFromBackupCR, *toRestore, crdsWithSubStatus)
}

func (h *handler) restoreNamespacedResources(ownerToDependentsList map[string][]restoreObj, toRestore *[]restoreObj,
	numOwnerReferences map[string]int, created map[string]bool, objFromBackupCR ObjectsFromBackupCR, crdsWithSubStatus []string) error {
	// generate adjacency lists for dependents and ownerRefs for namespaced resources
	if err := h.generateDependencyGraph(ownerToDependentsList, toRestore, numOwnerReferences, objFromBackupCR, created, namespaceScoped); err != nil {
		return err
	}
	return h.createFromDependencyGraph(ownerToDependentsList, created, numOwnerReferences, objFromBackupCR, *toRestore, crdsWithSubStatus)
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
	numOwnerReferences map[string]int, objFromBackupCR ObjectsFromBackupCR, created map[string]bool, scope string) error {
	var resourceInfoToData map[objInfo]unstructured.Unstructured
	switch scope {
	case clusterScoped:
		resourceInfoToData = objFromBackupCR.clusterscopedResourceInfoToData
	case namespaceScoped:
		resourceInfoToData = objFromBackupCR.namespacedResourceInfoToData
	}
	for resourceInfo, resourceData := range resourceInfoToData {
		name := resourceInfo.Name
		namespace := resourceInfo.Namespace
		gvr := resourceInfo.GVR
		if resourceData.GetKind() == "Deployment" && namespace == "cattle-system" {
			if strings.HasSuffix(name, "rancher") || strings.HasSuffix(name, "rancher-webhook") {
				logrus.Infof("Skip restoring the deployment %s/%s", namespace, name)
				continue
			}
		}
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
			// has no owners, so no need to add to adjacency list, add to restoreResources list
			*toRestore = append(*toRestore, currRestoreObj)
			continue
		}
		numOwners := 0
		logrus.Infof("Checking ownerRefs for resource %v of type %v", name, gvr.String())
		errCheckingOwnerRefs := false
		for _, owner := range ownerRefs {
			ownerRefData, ok := owner.(map[string]interface{})
			if !ok {
				errCheckingOwnerRefs = true
				logrus.Errorf("Invalid ownerRef for resource %v of type %v", name, gvr.String())
				continue
			}

			groupVersion := ownerRefData["apiVersion"].(string)
			gv, err := schema.ParseGroupVersion(groupVersion)
			if err != nil {
				errCheckingOwnerRefs = true
				logrus.Errorf("Error parsing ownerRef apiVersion %v for resource %v: %v", groupVersion, name, err)
				continue
			}
			kind := ownerRefData["kind"].(string)
			gvk := gv.WithKind(kind)
			logrus.Infof("Getting GVR for ownerRef %v of resource %v", gvk.String(), name)
			ownerGVR, isOwnerNamespaced, err := h.sharedClientFactory.ResourceForGVK(gvk)
			if err != nil {
				// Prior to Rancher 2.4.5, following resources had roles&rolebindings with malformed ownerRefs:
				// Secrets for cloud creds; NodeTemplates; ClusterTemplates & Revisions; Multiclusterapps & GlobalDNS
				// Kind was replaced by the resource name in plural and APIVersion field only contained the group and not version
				// Error is of the kind:  Kind=nodetemplates: no matches for kind "nodetemplates" in version "management.cattle.io"
				// this is an invalid ownerRef, can't restore current resource with this ownerRef. But if we continue and this resource has no valid ownerRef it won't get restored
				// so don't count this as owner. if the curr object has at least one valid ownerRef, it will get added to ownersToDependents list
				// if not, for objects like the rancher 2.4.5 nodetemplate, check at the end of this loop if even a single ownerRef is found, if not add it to toRestore list
				errCheckingOwnerRefs = true
				logrus.Errorf("Invalid ownerRef %v, either of the fields is incorrect: APIVersion or Kind", gvk.String())
				logrus.Errorf("Error getting ownerRef %v for object %v(of %v): %v", gvk.String(), name, gvr.String(), err)
				continue
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
			// If we are generating graph for the namespaced resources, and the ownerRef is clusterscoped, it should have been created by now
			// So we can check its presence in "created" map skip adding this ownerRef to ownerToDependentsList for the current resource
			if !isOwnerNamespaced {
				if created[ownerObj.ResourceConfigPath] {
					continue
				}
			}
			if isOwnerNamespaced {
				// if owner object is namespaced, then it has to be the same ns as the current dependent object as per k8s design
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
			numOwners++
		}
		if numOwners > 0 {
			numOwnerReferences[currRestoreObj.ResourceConfigPath] = numOwners
		} else {
			if !errCheckingOwnerRefs {
				// owners already exist (this will happen when generating dependency graph for namespaced resources that have
				// clusterscoped owners), so no need to add this namespaced resource to adjacency list, add to toRestore list
				*toRestore = append(*toRestore, currRestoreObj)
				continue
			}
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
	numOwnerReferences map[string]int, objFromBackupCR ObjectsFromBackupCR, toRestore []restoreObj, crdsWithSubStatus []string) error {
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
		target := fmt.Sprintf("%s.%s", currResourceInfo.GVR.Resource, currResourceInfo.GVR.GroupVersion().String())
		hasSubStatus := slice.ContainsString(crdsWithSubStatus, target)
		if err := h.restoreResource(currResourceInfo, resourceData, hasSubStatus); err != nil {
			logrus.Errorf("Error restoring resource %v of type %v: %v", currResourceInfo.Name, currResourceInfo.GVR.String(), err)
			errList = append(errList, fmt.Errorf("error restoring %v of type %v: %v", currResourceInfo.Name, currResourceInfo.GVR.String(), err))
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

	if len(toRestore) > 0 {
		// These resources could not be restored because of some issues with ownerRefs that violate k8s design
		for _, res := range toRestore {
			logrus.Warnf("Could not restore %v of type %v", res.Name, res.GVR.String())
		}
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
			logrus.Infof("Post-create: Updating status subresource for %#v of type %v", name, gvr)
			createdObj.Object["status"] = obj.Object["status"]
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
	updatedObj, err := dr.Update(h.ctx, &obj, k8sv1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("restoreResource: err updating resource %v", err)
	}
	if hasStatusSubresource {
		logrus.Infof("Updating status subresource for %#v of type %v", name, gvr)
		updatedObj.Object["status"] = obj.Object["status"]
		_, err := dr.UpdateStatus(h.ctx, updatedObj, k8sv1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("restoreResource: err updating status resource %v", err)
		}
	}

	logrus.Infof("Successfully restored %v", name)
	return nil
}

func (h *handler) updateOwnerRefs(ownerReferences []interface{}, namespace string) error {
	for ind, ownerRef := range ownerReferences {
		reference, ok := ownerRef.(map[string]interface{})
		if !ok {
			// can't be "!ok", but handling to avoid panic
			continue
		}
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
	if !condition.Cond(v1.RestoreConditionReconciling).IsUnknown(restore) && condition.Cond(v1.RestoreConditionReconciling).GetReason(restore) == "Error" {
		reconcileMsg := condition.Cond(v1.RestoreConditionReconciling).GetMessage(restore)
		if strings.Contains(reconcileMsg, originalErr.Error()) || strings.EqualFold(reconcileMsg, originalErr.Error()) {
			// no need to update object status again, because if another UpdateStatus is called without needing it, controller will
			// process the same object immediately without its default backoff
			return restore, originalErr
		}
	}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var err error
		updRestore, err := h.restores.Get(restore.Name, k8sv1.GetOptions{})
		if err != nil {
			return err
		}

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

func (h *handler) Lock(restore *v1.Restore) error {
	lease, err := h.kubernetesLeaseClient.Get(h.ctx, leaseName, k8sv1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		lease = &coordinationv1.Lease{
			ObjectMeta: k8sv1.ObjectMeta{
				Name:      leaseName,
				Namespace: util.ChartNamespace,
			},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity: leaseHolderName(restore),
			},
		}
		_, err = h.kubernetesLeaseClient.Create(h.ctx, lease, k8sv1.CreateOptions{})
		return err
	}

	if lease.Spec.HolderIdentity != nil {
		return fmt.Errorf("restore %v is in progress", *lease.Spec.HolderIdentity)
	}
	return h.updateLeaseHolderIdentity(restore, lease)
}

func (h *handler) updateLeaseHolderIdentity(restore *v1.Restore, lease *coordinationv1.Lease) error {
	lease.Spec.HolderIdentity = leaseHolderName(restore)
	_, err := h.kubernetesLeaseClient.Update(h.ctx, lease, k8sv1.UpdateOptions{})
	return err
}

func (h *handler) Unlock(id string) error {
	lease, err := h.kubernetesLeaseClient.Get(h.ctx, leaseName, k8sv1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	}
	if lease.Spec.HolderIdentity == nil {
		return nil
	}
	if *lease.Spec.HolderIdentity != id {
		return fmt.Errorf("restore %v cannot unlock lease, current lease holder is %v", id, *lease.Spec.HolderIdentity)
	}
	lease.Spec.HolderIdentity = nil
	_, err = h.kubernetesLeaseClient.Update(h.ctx, lease, k8sv1.UpdateOptions{})
	return err
}

func leaseHolderName(restore *v1.Restore) *string {
	return pointer.StringPtr(fmt.Sprintf("%s:%s", restore.Name, string(restore.UID)))
}

func getCRDsWithSubresourceStatus(crdData unstructured.Unstructured) (crdsWithSubresourceStatus []string) {
	specs := crdData.Object[specMapKey].(map[string]interface{})
	metadata := crdData.Object[metadataMapKey].(map[string]interface{})
	if subResources, ok := specs[subResourcesMapKey]; ok {
		// the case of apiVersion apiextensions.k8s.io/v1beta1
		if _, ok = subResources.(map[string]interface{})["status"]; ok {
			// example: crdVersion = clusterrepos.catalog.cattle.io/v1
			crdVersion := fmt.Sprintf("%s/%s", metadata["name"], specs["version"])
			crdsWithSubresourceStatus = append(crdsWithSubresourceStatus, crdVersion)
		}
	} else {
		// the case of apiVersion apiextensions.k8s.io/v1
		if versions, ok := specs[versionMapKey]; ok {
			for _, version := range versions.([]interface{}) {
				if subResources, ok := version.(map[string]interface{})[subResourcesMapKey]; ok {
					if _, ok = subResources.(map[string]interface{})["status"]; ok {
						crdVersion := fmt.Sprintf("%s/%s", metadata["name"], version.(map[string]interface{})["name"])
						crdsWithSubresourceStatus = append(crdsWithSubresourceStatus, crdVersion)
					}
				}
			}
		}
	}
	return crdsWithSubresourceStatus
}
