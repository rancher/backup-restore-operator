package restore

import (
	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/sirupsen/logrus"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

func (h *handler) scaleDownControllersFromResourceSet(objFromBackupCR ObjectsFromBackupCR) {
	for ind, controllerRef := range objFromBackupCR.backupResourceSet.ControllerReferences {
		controllerObj, dr := h.getObjFromControllerRef(controllerRef)
		if controllerObj == nil {
			continue
		}
		spec, specFound := controllerObj.Object["spec"].(map[string]interface{})
		if !specFound {
			logrus.WithFields(logrus.Fields{"name": controllerRef.Name}).Error("Controller reference spec not found, skipping resource")
			continue
		}
		replicas, int32replicaFound := spec["replicas"].(int32)
		if !int32replicaFound {
			int64replica, int64replicaFound := spec["replicas"].(int64)
			if !int64replicaFound {
				logrus.WithFields(logrus.Fields{"name": controllerRef.Name}).Error("Controller reference is invalid: replica count not found, skipping resource")
				continue
			}
			replicas = int32(int64replica)
		}
		// save the current replicas
		controllerRef.Replicas = replicas
		objFromBackupCR.backupResourceSet.ControllerReferences[ind] = controllerRef
		spec["replicas"] = 0
		// update controller to scale it down
		logrus.WithFields(logrus.Fields{"a_p_i_version": controllerRef.APIVersion, "resource": controllerRef.Resource, "name": controllerRef.Name}).Info("Scaling down controller to zero replicas")
		_, err := dr.Update(h.ctx, controllerObj, k8sv1.UpdateOptions{})
		if err != nil {
			logrus.WithFields(logrus.Fields{"a_p_i_version": controllerRef.APIVersion, "resource": controllerRef.Resource, "name": controllerRef.Name}).Error("Failed to scale down resource, skipping operation")
		}
	}
}

func (h *handler) scaleUpControllersFromResourceSet(objFromBackupCR ObjectsFromBackupCR) {
	for _, controllerRef := range objFromBackupCR.backupResourceSet.ControllerReferences {
		controllerObj, dr := h.getObjFromControllerRef(controllerRef)
		if controllerObj == nil {
			continue
		}
		controllerObj.Object["spec"].(map[string]interface{})["replicas"] = controllerRef.Replicas
		// update controller to scale it back up
		logrus.WithFields(logrus.Fields{"a_p_i_version": controllerRef.APIVersion, "resource": controllerRef.Resource, "name": controllerRef.Name, "replicas": controllerRef.Replicas}).Info("Scaling up controller to target replica count")
		_, err := dr.Update(h.ctx, controllerObj, k8sv1.UpdateOptions{})
		if err != nil {
			logrus.WithFields(logrus.Fields{"a_p_i_version": controllerRef.APIVersion, "resource": controllerRef.Resource, "name": controllerRef.Name, "replicas": controllerRef.Replicas}).Error("Failed to scale up controller, manual intervention required to scale back to desired replica count")
		}
	}
}

func (h *handler) getObjFromControllerRef(controllerRef v1.ControllerReference) (*unstructured.Unstructured, dynamic.ResourceInterface) {
	logrus.WithFields(logrus.Fields{"a_p_i_version": controllerRef.APIVersion, "resource": controllerRef.Resource, "name": controllerRef.Name}).Info("Processing controller reference with specified API version, resource type, and name")
	var dr dynamic.ResourceInterface
	gv, err := schema.ParseGroupVersion(controllerRef.APIVersion)
	if err != nil {
		logrus.WithFields(logrus.Fields{"a_p_i_version": controllerRef.APIVersion, "name": controllerRef.Name}).Error("Failed to parse API version for controller reference, skipping resource")
		return nil, dr
	}
	gvr := gv.WithResource(controllerRef.Resource)
	dr = h.dynamicClient.Resource(gvr)
	if controllerRef.Namespace != "" {
		dr = h.dynamicClient.Resource(gvr).Namespace(controllerRef.Namespace)
	}
	controllerObj, err := dr.Get(h.ctx, controllerRef.Name, k8sv1.GetOptions{})
	if err != nil {
		logrus.WithFields(logrus.Fields{"a_p_i_version": controllerRef.APIVersion, "resource": controllerRef.Resource, "name": controllerRef.Name, "error": err}).Debug("Failed to retrieve Kubernetes object referenced by controller")
		return nil, dr
	}
	return controllerObj, dr
}
