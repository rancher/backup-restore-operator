package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"os"
	"path/filepath"
	"strings"

	//"github.com/kubernetes/kubernetes/pkg/features"
	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	common "github.com/mrajashree/backup/pkg/controllers"
	backupControllers "github.com/mrajashree/backup/pkg/generated/controllers/backupper.cattle.io/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
)

var defaultGroupVersionsToBackup = []string{"v1", "rbac.authorization.k8s.io/v1", "management.cattle.io/v3", "project.cattle.io/v3"}

type handler struct {
	backups         backupControllers.BackupController
	discoveryClient discovery.DiscoveryInterface
	dynamicClient   dynamic.Interface
}

var avoidBackupResources = map[string]bool{"pods": true}

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
		groupVersionsToBackup = strings.Split(backup.Spec.GroupVersions, ",")
	}
	// TODO: get objectStore details too
	backupPath := backup.Spec.Local
	backupInfo, err := os.Stat(backupPath)
	if err == nil && backupInfo.IsDir() {
		return backup, nil
	}
	err = os.Mkdir(backupPath, os.ModePerm)
	if err != nil {
		return backup, fmt.Errorf("error creating temp dir: %v", err)
	}
	ownerDirPath := backupPath + "/owners"
	err = os.Mkdir(ownerDirPath, os.ModePerm)
	if err != nil {
		return backup, fmt.Errorf("error creating temp dir: %v", err)
	}
	dependentDirPath := backupPath + "/dependents"
	err = os.Mkdir(dependentDirPath, os.ModePerm)
	if err != nil {
		return backup, fmt.Errorf("error creating temp dir: %v", err)
	}
	//h.discoveryClient.ServerGroupsAndResources()
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
			if avoidBackupResources[res.Name] {
				continue
			}
			if !canListResource(res.Verbs) {
				fmt.Printf("\nCannot list resource %v\n", res)
				continue
			}
			if !canUpdateResource(res.Verbs) {
				fmt.Printf("\nCannot update resource %v\n", res)
				continue
			}

			gvr := gv.WithResource(res.Name)
			fmt.Printf("\nBacking up items for resource %v with gvr %v\n", res, gvr)
			var dr dynamic.ResourceInterface
			dr = h.dynamicClient.Resource(gvr)
			// TODO: which context to use
			ctx := context.Background()
			// TODO: use single version to get consistent backup
			//etcdVersioner := etcd3.APIObjectVersioner{}
			//rev, err := etcdVersioner.ObjectResourceVersion(res.)
			//if err != nil {
			//	t.Fatal(err)
			//}
			resObjects, err := dr.List(ctx, k8sv1.ListOptions{})
			if err != nil {
				return backup, err
			}
			for _, resObj := range resObjects.Items {
				currObjLabels := resObj.Object["metadata"].(map[string]interface{})["labels"]
				if resObj.Object["metadata"].(map[string]interface{})["uid"] != nil {
					oidLabel := map[string]string{common.OldUIDReferenceLabel: resObj.Object["metadata"].(map[string]interface{})["uid"].(string)}
					if currObjLabels == nil {
						resObj.Object["metadata"].(map[string]interface{})["labels"] = oidLabel
					} else {
						currLabels := currObjLabels.(map[string]interface{})
						currLabels[common.OldUIDReferenceLabel] = resObj.Object["metadata"].(map[string]interface{})["uid"].(string)
						resObj.Object["metadata"].(map[string]interface{})["labels"] = currLabels
					}
				}
				delete(resObj.Object["metadata"].(map[string]interface{}), "uid")
				delete(resObj.Object["metadata"].(map[string]interface{}), "resourceVersion")
				if resObj.Object["metadata"].(map[string]interface{})["ownerReferences"] == nil {
					resourcePath := ownerDirPath + "/" + res.Name + "." + gv.Group + "#" + gv.Version
					if err := createResourceDir(resourcePath); err != nil {
						return backup, err
					}
					_, err = writeToFile(resObj.Object, resourcePath, resObj.Object["metadata"].(map[string]interface{})["name"].(string))
				} else {
					resourcePath := dependentDirPath + "/" + res.Name + "." + gv.Group + "#" + gv.Version
					if err := createResourceDir(resourcePath); err != nil {
						return backup, err
					}
					_, err = writeToFile(resObj.Object, resourcePath, resObj.Object["metadata"].(map[string]interface{})["name"].(string))
				}
			}
		}
	}
	return backup, nil
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

func writeToFile(item map[string]interface{}, backupPath, pattern string) (string, error) {
	f, err := os.Create(filepath.Join(backupPath, filepath.Base(pattern+".json")))
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

func canUpdateResource(verbs k8sv1.Verbs) bool {
	for _, v := range verbs {
		if v == "update" || v == "patch" {
			return true
		}
	}
	return false
}
