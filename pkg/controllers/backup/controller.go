package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"os"

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
	// TODO: get objectStore details too
	backupPath := backup.Spec.Local
	fmt.Printf("\nbackupPath: %v\n", backupPath)
	err := os.Mkdir(backupPath, os.ModePerm)
	if err != nil {
		return backup, fmt.Errorf("error creating temp dir: %v", err)
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
			fmt.Printf("\nBacking up items for resource %v\n", res)
			gvr := gv.WithResource(res.Name)
			var dr dynamic.ResourceInterface
			dr = h.dynamicClient.Resource(gvr)
			// TODO: which context to use
			ctx := context.Background()
			// TODO: use single version to get consistent backup
			resObjects, err := dr.List(ctx, k8sv1.ListOptions{})
			if err != nil {
				return backup, err
			}
			for _, resObj := range resObjects.Items {
				fmt.Printf("%v\n", resObj.Object["metadata"].(map[string]interface{})["name"])
				fmt.Printf("%v\n", resObj.Object["metadata"].(map[string]interface{})["resourceVersion"])
				fmt.Printf("labels: %v\n", resObj.Object["metadata"].(map[string]interface{})["labels"])
				currObjLabels := resObj.Object["metadata"].(map[string]interface{})["labels"]
				if resObj.Object["metadata"].(map[string]interface{})["uid"] != nil {
					oidLabel := map[string]string{"backupper.cattle.io/old-uid": resObj.Object["metadata"].(map[string]interface{})["uid"].(string)}
					if currObjLabels == nil {
						resObj.Object["metadata"].(map[string]interface{})["labels"] = oidLabel
					} else {
						currLabels := currObjLabels.(map[string]interface{})
						currLabels["backupper.cattle.io/old-uid"] = resObj.Object["metadata"].(map[string]interface{})["uid"].(string)
						resObj.Object["metadata"].(map[string]interface{})["labels"] = currLabels
					}
				}
				delete(resObj.Object["metadata"].(map[string]interface{}), "uid")

				_, err = writeToFile(resObj.Object, backupPath, resObj.Object["metadata"].(map[string]interface{})["name"].(string))
			}
		}
	}
	return backup, nil
}

// from velero https://github.com/vmware-tanzu/velero/blob/master/pkg/backup/item_collector.go#L267
func writeToFile(item map[string]interface{}, backupPath, pattern string) (string, error) {
	f, err := ioutil.TempFile(backupPath, pattern)
	if err != nil {
		return "", fmt.Errorf("error creating temp file: %v", err)
	}
	defer f.Close()

	jsonBytes, err := json.Marshal(item)
	if err != nil {
		return "", fmt.Errorf("error converting item to JSON: %v", err)
	}

	if _, err := f.Write(jsonBytes); err != nil {
		return "", fmt.Errorf("error writing JSON to file: %v", err)
	}

	if err := f.Close(); err != nil {
		return "", fmt.Errorf("error closing file: %v", err)
	}

	return f.Name(), nil
}

func canListResource(verbs k8sv1.Verbs) bool {
	for _, v := range verbs {
		if v == "list" {
			return true
		}
	}
	return false
}
