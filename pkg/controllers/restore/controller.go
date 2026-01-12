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
	"github.com/rancher/backup-restore-operator/pkg/util/encryptionconfig"
	lasso "github.com/rancher/lasso/pkg/client"
	v1core "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	"github.com/rancher/wrangler/v3/pkg/slice"
	"github.com/sirupsen/logrus"

	coordinationv1 "k8s.io/api/coordination/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	k8sEncryptionconfig "k8s.io/apiserver/pkg/server/options/encryptionconfig"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	coordinationclientv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/pointer"
)

const (
	metadataMapKey                = "metadata"
	ownerRefsMapKey               = "ownerReferences"
	clusterScoped                 = "clusterscoped"
	deletionGracePeriodSecondsKey = "deletionGracePeriodSeconds"
	namespaceScoped               = "namespaceScoped"
	leaseName                     = "restore-controller"
	preserveUnknownFieldsKey      = "preserveUnknownFields"
	secretsMapKey                 = "secrets"
	specMapKey                    = "spec"
	subResourcesMapKey            = "subresources"
	versionMapKey                 = "versions"
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
	metricsServerEnabled    bool
	encryptionProviderPath  string
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
	defaultS3 *v1.S3ObjectStore,
	metricsServerEnabled bool,
	encryptionProviderPath string) {

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
		metricsServerEnabled:    metricsServerEnabled,
		encryptionProviderPath:  encryptionProviderPath,
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

	logrus.WithFields(logrus.Fields{"name": restore.Name}).Info("Processing restore custom resource")
	var backupSource string
	backupName := restore.Spec.BackupFilename
	logrus.WithFields(logrus.Fields{"backup_filename": restore.Spec.BackupFilename}).Info("Initiating database restore from backup file")

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

	transformerMap := k8sEncryptionconfig.StaticTransformers{}
	var err error
	if restore.Spec.EncryptionConfigSecretName != "" {
		logrus.WithFields(logrus.Fields{"encryption_config_secret_name": restore.Spec.EncryptionConfigSecretName, "name": restore.Name}).Info("Processing encryption configuration for restore custom resource")
		encryptionConfigSecret, err := encryptionconfig.GetEncryptionConfigSecret(h.secrets, restore.Spec.EncryptionConfigSecretName)
		if err != nil {
			logrus.WithFields(logrus.Fields{"error": err}).Error("Failed to fetch encryption configuration secret")
			return h.setReconcilingCondition(restore, err)
		}

		transformerMap, err = encryptionconfig.GetEncryptionTransformersFromSecret(h.ctx, encryptionConfigSecret, h.encryptionProviderPath)
		if err != nil {
			logrus.WithFields(logrus.Fields{"error": err}).Error("Failed to process encryption configuration")
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
		return h.setReconcilingCondition(restore, fmt.Errorf("backup location not specified on the restore CR, and not configured at the operator level"))
	}

	// first stop the controllers
	h.scaleDownControllersFromResourceSet(objFromBackupCR)

	// first restore CRDs
	logrus.WithFields(logrus.Fields{"name": restore.Name}).Info("Starting CRD restoration process for restore resource")
	if crdsWithSubStatus, err = h.restoreCRDs(created, objFromBackupCR); err != nil {
		h.scaleUpControllersFromResourceSet(objFromBackupCR)
		if restore.Spec.IgnoreErrors {
			logrus.WithFields(logrus.Fields{"error": err}).Warn("Failed to restore CRDs during migration, continuing with remaining operations")
		} else {
			logrus.WithFields(logrus.Fields{"error": err}).Error("Failed to restore custom resource definitions during migration process")
			// Cannot set the exact error on reconcile condition, the order in which resources failed to restore are added in err msg could
			// change with each restore, which means the condition will get updated on each try
			return h.setReconcilingCondition(restore, fmt.Errorf("error restoring CRDs, check logs for exact error"))
		}
	}

	logrus.WithFields(logrus.Fields{"name": restore.Name}).Info("Starting cluster-scoped resource restoration for restore CR")
	// then restore clusterscoped resources, by first generating dependency graph for cluster scoped resources, and create from the graph
	if err := h.restoreClusterScopedResources(ownerToDependentsList, &toRestore, numOwnerReferences, created, objFromBackupCR, crdsWithSubStatus); err != nil {
		h.scaleUpControllersFromResourceSet(objFromBackupCR)
		if restore.Spec.IgnoreErrors {
			logrus.WithFields(logrus.Fields{"error": err}).Warn("Failed to restore cluster-scoped resources, continuing with migration")
		} else {
			logrus.WithFields(logrus.Fields{"error": err}).Error("Failed to restore cluster-scoped resources")
			return h.setReconcilingCondition(restore, fmt.Errorf("error restoring cluster-scoped resources, check logs for exact error"))
		}
	}

	logrus.WithFields(logrus.Fields{"name": restore.Name}).Info("Starting restore of namespaced resources for custom resource")
	// now restore namespaced resources: generate adjacency lists for dependents and ownerRefs for namespaced resources
	ownerToDependentsList = make(map[string][]restoreObj)
	toRestore = []restoreObj{}
	if err := h.restoreNamespacedResources(ownerToDependentsList, &toRestore, numOwnerReferences, created, objFromBackupCR, crdsWithSubStatus); err != nil {
		h.scaleUpControllersFromResourceSet(objFromBackupCR)
		if restore.Spec.IgnoreErrors {
			logrus.WithFields(logrus.Fields{"error": err}).Warn("Failed to restore namespaced resources but continuing with migration process")
		} else {
			logrus.WithFields(logrus.Fields{"error": err}).Error("Failed to restore namespaced resources")
			return h.setReconcilingCondition(restore, fmt.Errorf("error restoring namespaced resources, check logs for exact error"))
		}
	}

	// prune by default
	if restore.Spec.Prune == nil || *restore.Spec.Prune == true {
		logrus.WithFields(logrus.Fields{"name": restore.Name}).Info("Pruning resources not included in backup for restore operation")
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
		v1.RestoreConditionReady.SetStatusBool(restore, true)
		v1.RestoreConditionReady.Message(restore, "Completed")

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
			logrus.WithFields(logrus.Fields{"crds": crds}).Debug("Adding CRDs to status subresource list")
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
	logrus.WithFields(logrus.Fields{"crd_name": crdName}).Info("Waiting for custom resource definition to become available")
	defer logrus.WithFields(logrus.Fields{"crd_name": crdName}).Info("CRD availability wait completed successfully")

	first := true
	return wait.Poll(500*time.Millisecond, 60*time.Second, func() (bool, error) {
		if !first {
			logrus.WithFields(logrus.Fields{"crd_name": crdName}).Info("Waiting for custom resource definition to become available")
		}
		first = false

		crd, err := h.apiClient.ApiextensionsV1().CustomResourceDefinitions().Get(h.ctx, crdName, k8sv1.GetOptions{})
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
					logrus.WithFields(logrus.Fields{"crd_name": crdName, "reason": cond.Reason}).Info("CRD name conflict detected during processing")
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
				logrus.WithFields(logrus.Fields{"namespace": namespace, "name": name}).Info("Skipping deployment restoration due to existing configuration")
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

		customize(&resourceData)

		metadata := resourceData.Object[metadataMapKey].(map[string]interface{})
		ownerRefs, ownerRefsFound := metadata[ownerRefsMapKey].([]interface{})
		if !ownerRefsFound {
			// has no owners, so no need to add to adjacency list, add to restoreResources list
			*toRestore = append(*toRestore, currRestoreObj)
			continue
		}
		numOwners := 0
		logrus.WithFields(logrus.Fields{"name": name, "string": gvr.String()}).Info("Checking owner references for resource with specified type")
		errCheckingOwnerRefs := false
		for _, owner := range ownerRefs {
			ownerRefData, ok := owner.(map[string]interface{})
			if !ok {
				errCheckingOwnerRefs = true
				logrus.WithFields(logrus.Fields{"name": name, "string": gvr.String()}).Error("Invalid owner reference found for resource with specified type")
				continue
			}

			groupVersion := ownerRefData["apiVersion"].(string)
			gv, err := schema.ParseGroupVersion(groupVersion)
			if err != nil {
				errCheckingOwnerRefs = true
				logrus.WithFields(logrus.Fields{"group_version": groupVersion, "name": name, "error": err}).Error("Failed to parse owner reference API version for resource")
				continue
			}
			kind := ownerRefData["kind"].(string)
			gvk := gv.WithKind(kind)
			logrus.WithFields(logrus.Fields{"string": gvk.String(), "name": name}).Info("Resolving group version resource for owner reference")
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
				logrus.WithFields(logrus.Fields{"string": gvk.String()}).Error("Invalid owner reference detected with incorrect APIVersion or Kind field")
				logrus.WithFields(logrus.Fields{"string": gvk.String(), "name": name, "string": gvr.String(), "error": err}).Error("Failed to retrieve owner reference for object: check object permissions and API connectivity")
				continue
			}

			// This behavior is needed as BRO ignores all builtin GlobalRoles and RoleTemplates which would lead to all their child resources
			// not beind properly created on migrations if they kept waiting for their Owners to be created.
			if strings.EqualFold(kind, "globalrole") || strings.EqualFold(kind, "roletemplate") {
				errCheckingOwnerRefs = true
				logrus.WithFields(logrus.Fields{"name": name, "string": gvr.String(), "string": gvk.String()}).Info("Resource owner references will be dropped during migration due to type mismatch")
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
			logrus.WithFields(logrus.Fields{"name": name, "string": gvr.String()}).Warn("Resource with invalid owner references added to restore queue with references dropped")
			delete(currRestoreObj.Data.Object[metadataMapKey].(map[string]interface{}), ownerRefsMapKey)
			*toRestore = append(*toRestore, currRestoreObj)
		}
	}
	return nil
}

// customize provides customization of restored resource for edge cases
func customize(obj *unstructured.Unstructured) {
	switch obj.GetKind() {
	case "ServiceAccount":
		// remove secrets section as referenced secrets will be removed by k8s Token Controller as they are considered orphaned
		delete(obj.Object, secretsMapKey)
		logrus.WithFields(logrus.Fields{"get_namespace": obj.GetNamespace(), "get_name": obj.GetName()}).Debug("Secrets section ignored for ServiceAccount resource during processing")
	case "Cluster":
		// for fleet cluster it needs to be reimported in order to reissue service account token that is no longer valid
		switch obj.GetAPIVersion() {
		case "fleet.cattle.io/v1alpha1":
			// only warn error if field can't be found. In case there are breaking api changes error will be printed as Warn and user has workaround to patch it.
			redeployAgentGeneration, _, err := unstructured.NestedFloat64(obj.Object, "spec", "redeployAgentGeneration")
			if err != nil {
				logrus.WithFields(logrus.Fields{"get_namespace": obj.GetNamespace(), "get_name": obj.GetName(), "error": err}).Warn("Failed to reset fleet cluster for re-import: unable to fetch spec.redeployAgentGeneration field")
				return
			}
			if err := unstructured.SetNestedField(obj.Object, int64(redeployAgentGeneration+1), "spec", "redeployAgentGeneration"); err != nil {
				logrus.WithFields(logrus.Fields{"get_namespace": obj.GetNamespace(), "get_name": obj.GetName(), "error": err}).Warn("Failed to reset fleet cluster for re-import due to error")
			}
		case "provisioning.cattle.io/v1":
			redeploySystemAgentGeneration, _, err := unstructured.NestedFloat64(obj.Object, "spec", "redeploySystemAgentGeneration")
			if err != nil {
				logrus.WithFields(logrus.Fields{"get_namespace": obj.GetNamespace(), "get_name": obj.GetName(), "error": err}).Warn("Failed to reset provisioning cluster for re-import: unable to fetch spec.redeploySystemAgentGeneration")
				return
			}
			if err := unstructured.SetNestedField(obj.Object, int64(redeploySystemAgentGeneration+1), "spec", "redeploySystemAgentGeneration"); err != nil {
				logrus.WithFields(logrus.Fields{"get_namespace": obj.GetNamespace(), "get_name": obj.GetName(), "error": err}).Warn("Failed to reset provisioning cluster for re-import due to configuration error")
			}
		case "management.cattle.io/v3":
			//Set io.cattle.agent.force.deploy to true to force cattle-cluster-agent redeployment
			annotations := obj.GetAnnotations()
			annotations["io.cattle.agent.force.deploy"] = "true"
			obj.SetAnnotations(annotations)
		}
	}
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
			logrus.WithFields(logrus.Fields{"resource_config_path": curr.ResourceConfigPath}).Info("Resource already exists or has been updated at the specified configuration path")
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
			logrus.WithFields(logrus.Fields{"name": currResourceInfo.Name, "string": currResourceInfo.GVR.String(), "error": err}).Error("Failed to restore resource during migration process")
			errList = append(errList, fmt.Errorf("error restoring %v of type %v: %v", currResourceInfo.Name, currResourceInfo.GVR.String(), err))
			continue
		}
		for _, dependent := range ownerToDependentsList[curr.ResourceConfigPath] {
			// example, curr = catTemplate, dependent=catTempVer
			if numOwnerReferences[dependent.ResourceConfigPath] > 0 {
				numOwnerReferences[dependent.ResourceConfigPath]--
			}
			if numOwnerReferences[dependent.ResourceConfigPath] == 0 {
				logrus.WithFields(logrus.Fields{"name": dependent.Name}).Info("Dependent service is now ready for creation")
				toRestore = append(toRestore, dependent)
			}
		}
		created[curr.ResourceConfigPath] = true
		countRestored++
	}

	if len(toRestore) > 0 {
		// These resources could not be restored because of some issues with ownerRefs that violate k8s design
		for _, res := range toRestore {
			logrus.WithFields(logrus.Fields{"name": res.Name, "string": res.GVR.String()}).Warn("Failed to restore resource: operation could not be completed")
		}
	}

	return util.ErrList(errList)
}

func (h *handler) restoreResource(restoreObjInfo objInfo, restoreObjData unstructured.Unstructured, hasStatusSubresource bool) error {
	logrus.WithFields(logrus.Fields{"name": restoreObjInfo.Name, "g_v_r": restoreObjInfo.GVR}).Info("Starting resource restoration for specified object type")

	fileMap := restoreObjData.Object
	obj := restoreObjData

	fileMapMetadata := fileMap[metadataMapKey].(map[string]interface{})
	name := restoreObjInfo.Name
	namespace := restoreObjInfo.Namespace
	gvr := restoreObjInfo.GVR
	// TEMPORARY HOTFIX: Don't restore secrets of type fleet.cattle.io/cluster-registration-values
	if gvr.Resource == "secrets" {
		secretType, found, err := unstructured.NestedString(obj.Object, "type")
		if err != nil {
			return err
		}
		if found && secretType == "fleet.cattle.io/cluster-registration-values" {
			logrus.WithFields(logrus.Fields{"namespace": namespace, "name": name}).Info("Skipping secret restore due to cluster registration type")
			return nil
		}
	}
	var dr dynamic.ResourceInterface
	dr = h.dynamicClient.Resource(gvr)
	if namespace != "" {
		dr = h.dynamicClient.Resource(gvr).Namespace(namespace)
		logrus.WithFields(logrus.Fields{"namespace": namespace, "name": restoreObjInfo.Name, "g_v_r": restoreObjInfo.GVR}).Info("Restoring resource in namespace with specified name and type")
	}
	ownerReferences, _ := fileMapMetadata[ownerRefsMapKey].([]interface{})
	if ownerReferences != nil {
		if err := h.updateOwnerRefs(ownerReferences, namespace); err != nil {
			if apierrors.IsNotFound(err) {
				// This can only happen when the ownerRefs are created in a way that violates k8s design https://kubernetes.io/docs/concepts/workloads/controllers/garbage-collection/
				// Although disallowed, k8s currently has a bug where it allows creating cross-namespaced ownerRefs, and lets create clusterscoped objects with namespaced owners
				// https://github.com/kubernetes/kubernetes/issues/65200
				logrus.WithFields(logrus.Fields{"name": name}).Warn("Failed to find owner reference for resource, check resource configuration")
				// if owner not found, still restore resource but drop the ownerRefs field,
				// because k8s terminates objects with invalid ownerRef UIDs
				delete(obj.Object[metadataMapKey].(map[string]interface{}), ownerRefsMapKey)
				logrus.WithFields(logrus.Fields{"name": name}).Warn("Resource will be restored without owner references; manually edit to add required references")
			} else {
				return err
			}
		}
	}
	// Check for invalid v1beta1 fields if the APIVersion is v1
	if obj.GetAPIVersion() == "apiextensions.k8s.io/v1" {
		// Invalid field is spec.preserveUnknownFields
		if _, ok := obj.Object["spec"].(map[string]interface{})[preserveUnknownFieldsKey]; ok {
			logrus.WithFields(logrus.Fields{"name": restoreObjInfo.Name, "g_v_r": restoreObjInfo.GVR}).Info("Marking resource for migration from current version to valid v1 format")
			// Set spec.preserveUnknownFields to false
			unstructured.SetNestedField(obj.Object, false, "spec", preserveUnknownFieldsKey)
			// New fields to be added to replace spec.preserveUnknownFields
			// schema.openAPIV3Schema.type = object
			// schema.openAPIV3Schema.x-kubernetes-preserve-unknown-fields = true
			var preserveUnknownFields = map[string]interface{}{
				"type":                                 "object",
				"x-kubernetes-preserve-unknown-fields": true,
			}
			setValidationOverride(&obj, preserveUnknownFields)
		}
	}
	// Drop immutable metadata.deletionGracePeriodSeconds
	deletionGracePeriodSeconds, _ := fileMapMetadata[deletionGracePeriodSecondsKey]
	logrus.WithFields(logrus.Fields{"name": restoreObjInfo.Name, "g_v_r": restoreObjInfo.GVR, "deletion_grace_period_seconds": deletionGracePeriodSeconds}).Trace("Deletion grace period configured for Kubernetes resource")
	if deletionGracePeriodSeconds != nil {
		logrus.WithFields(logrus.Fields{"name": restoreObjInfo.Name, "g_v_r": restoreObjInfo.GVR}).Info("Removing immutable metadata.deletionGracePeriodSeconds field during object restoration")
		delete(obj.Object[metadataMapKey].(map[string]interface{}), deletionGracePeriodSecondsKey)
	}
	logrus.WithFields(logrus.Fields{"obj": obj}).Trace("Restoring resource object")

	res, err := dr.Get(h.ctx, name, k8sv1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("restoreResource: err getting resource %v", err)
		}
		// create and return
		createdObj, err := dr.Create(h.ctx, &obj, k8sv1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("restoreResource: err creating resource %v", err)
		}
		if hasStatusSubresource && obj.Object["status"] != nil {
			logrus.WithFields(logrus.Fields{"name": name, "gvr": gvr}).Info("Post-create status subresource update initiated for resource")
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
	if hasStatusSubresource && obj.Object["status"] != nil {
		logrus.WithFields(logrus.Fields{"name": name, "gvr": gvr}).Info("Updating status subresource for resource with specified group, version, and kind")
		updatedObj.Object["status"] = obj.Object["status"]
		_, err := dr.UpdateStatus(h.ctx, updatedObj, k8sv1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("restoreResource: err updating status resource %v", err)
		}
	}

	logrus.WithFields(logrus.Fields{"name": name}).Info("Database backup successfully restored for resource")
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

		logrus.WithFields(logrus.Fields{"name": ownerObj.Name}).Info("Generating new UID for owner object")
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
	if !v1.RestoreConditionReconciling.IsUnknown(restore) && v1.RestoreConditionReconciling.GetReason(restore) == "Error" {
		reconcileMsg := v1.RestoreConditionReconciling.GetMessage(restore)
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

		v1.RestoreConditionReconciling.SetStatusBool(updRestore, true)
		v1.RestoreConditionReconciling.SetError(updRestore, "", originalErr)
		v1.BackupConditionReady.Message(updRestore, "Retrying")

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
				Namespace: util.GetChartNamespace(),
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

// Thanks to https://github.com/argoproj/argo-rollouts/blob/4ee03654642c90e8970a9524d5da1623d6777399/hack/gen-crd-spec/main.go#L45
func setValidationOverride(un *unstructured.Unstructured, fieldOverride map[string]interface{}) {
	// Prepare variables
	preSchemaPath := []string{"spec", "versions"}
	objVersions, _, _ := unstructured.NestedSlice(un.Object, preSchemaPath...)

	schemaPath := []string{"schema", "openAPIV3Schema"}

	// Loop over version's slice
	var finalOverride []interface{}
	for _, v := range objVersions {
		unstructured.SetNestedMap(v.(map[string]interface{}), fieldOverride, schemaPath...)

		_, ok, err := unstructured.NestedFieldNoCopy(v.(map[string]interface{}), schemaPath...)
		if err != nil {
			logrus.WithFields(logrus.Fields{"schema_path": schemaPath, "crd_kind": crdKind(), "error": err}).Error("Failed to retrieve nested schema field for CRD kind")
			continue
		}
		if !ok {
			logrus.WithFields(logrus.Fields{"schema_path": schemaPath, "crd_kind": crdKind()}).Error("Schema file not found for the specified CRD kind")
			continue
		}
		finalOverride = append(finalOverride, v)
	}

	// Write back to top object
	unstructured.SetNestedSlice(un.Object, finalOverride, preSchemaPath...)
}

// Thanks to https://github.com/argoproj/argo-rollouts/blob/4ee03654642c90e8970a9524d5da1623d6777399/hack/gen-crd-spec/main.go#L127
func crdKind(crd *unstructured.Unstructured) string {
	kind, found, err := unstructured.NestedFieldNoCopy(crd.Object, "spec", "names", "kind")
	if err != nil {
		logrus.WithFields(logrus.Fields{"object": crd.Object, "error": err}).Error("Failed to retrieve spec.names.kind field from CRD object")
		return ""
	}

	if !found {
		logrus.WithFields(logrus.Fields{"object": crd.Object}).Error("Failed to determine kind for CRD object during processing")
		return ""
	}
	return kind.(string)
}
