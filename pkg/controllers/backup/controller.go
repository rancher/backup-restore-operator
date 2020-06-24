package backup

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	//"github.com/kubernetes/kubernetes/pkg/features"
	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"

	//v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	backupControllers "github.com/mrajashree/backup/pkg/generated/controllers/backupper.cattle.io/v1"
)

var defaultGroupVersionsToBackup = []string{"v1", "rbac.authorization.k8s.io/v1", "management.cattle.io/v3", "project.cattle.io/v3"}

type handler struct {
	backups         backupControllers.BackupController
	discoveryClient discovery.DiscoveryInterface
	dynamicClient   dynamic.Interface
}

func Register(
	ctx context.Context,
	backups backupControllers.BackupController,
	clientSet *clientset.Clientset,
	dynamicInterface dynamic.Interface) {

	controller := &handler{
		backups:         backups,
		discoveryClient: clientSet.Discovery(),
		dynamicClient:   dynamicInterface,
	}

	// Register handlers
	backups.OnChange(ctx, "backups", controller.OnBackupChange)
	//backups.OnRemove(ctx, controllerRemoveName, controller.OnEksConfigRemoved)
}

func (h *handler) OnBackupChange(_ string, backup *v1.Backup) (*v1.Backup, error) {
	var groupVersionsToBackup []string
	if len(backup.Spec.GroupVersions) == 0 {
		// use default
		groupVersionsToBackup = defaultGroupVersionsToBackup
	} else {
		groupVersionsToBackup = backup.Spec.GroupVersions
	}
	for _, gv := range groupVersionsToBackup {
		resources, err := h.discoveryClient.ServerResourcesForGroupVersion(gv)
		if err != nil {
			return backup, err
		}
		gv, err := schema.ParseGroupVersion(gv)
		if err != nil {
			return backup, err
		}
		fmt.Printf("\nBacking up resources for groupVersion %v\n", gv)
		for _, res := range resources.APIResources {
			if !canListResource(res.Verbs) {
				continue
			}

			gvr := gv.WithResource(res.Name)
			var dr dynamic.ResourceInterface
			dr = h.dynamicClient.Resource(gvr)
			// TODO: which context to use
			ctx := context.Background()
			resObjects, err := dr.List(ctx, k8sv1.ListOptions{})
			if err != nil {
				return backup, err
			}
			for _, resObj := range resObjects.Items {
				fmt.Printf("%v\n", resObj.Object["metadata"].(map[string]interface{})["name"])
				fmt.Printf("%v\n", resObj.Object["metadata"].(map[string]interface{})["resourceVersion"])
			}
		}
	}
	return backup, nil
}

func canListResource(verbs k8sv1.Verbs) bool {
	for _, v := range verbs {
		if v == "list" {
			return true
		}
	}
	return false
}
