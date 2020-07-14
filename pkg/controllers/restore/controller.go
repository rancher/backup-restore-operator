package restore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	common "github.com/mrajashree/backup/pkg/controllers"
	"io/ioutil"
	"strings"

	//common "github.com/mrajashree/backup/pkg/controllers"
	backupControllers "github.com/mrajashree/backup/pkg/generated/controllers/backupper.cattle.io/v1"
	//"io/ioutil"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"os/exec"
	"path/filepath"
	//"strings"
)

type handler struct {
	restores        backupControllers.RestoreController
	backups         backupControllers.BackupController
	discoveryClient discovery.DiscoveryInterface
	dynamicClient   dynamic.Interface
}

func Register(
	ctx context.Context,
	restores backupControllers.RestoreController,
	backups backupControllers.BackupController,
	clientSet *clientset.Clientset,
	dynamicInterface dynamic.Interface) {

	controller := &handler{
		restores:        restores,
		backups:         backups,
		dynamicClient:   dynamicInterface,
		discoveryClient: clientSet.Discovery(),
	}

	// Register handlers
	restores.OnChange(ctx, "restore", controller.OnRestoreChange)
	//backups.OnRemove(ctx, controllerRemoveName, controller.OnEksConfigRemoved)
}

func (h *handler) OnRestoreChange(_ string, restore *v1.Restore) (*v1.Restore, error) {
	backupName := restore.Spec.BackupName
	backup, err := h.backups.Get("default", backupName, k8sv1.GetOptions{})
	if err != nil {
		return restore, err
	}
	// TODO: logic to read from object store
	backupPath := backup.Spec.Local

	// first restore namespaces
	nsPath := filepath.Join(backupPath, "namespaces")
	cmdNs := exec.Command("kubectl", "apply", "-f", nsPath, "--recursive")
	var out, errB bytes.Buffer
	cmdNs.Stdout = &out
	cmdNs.Stderr = &errB
	//fmt.Printf("\nrunning command %v\n", cmd.String())
	if err := cmdNs.Run(); err != nil {
		//fmt.Printf("output: %q\n", out.String())
		fmt.Printf("error: %q\n", errB.String())
		return restore, err
	}
	// then restore CRDs
	CRDPath := filepath.Join(backupPath, "customresourcedefinitions")
	cmdCRD := exec.Command("kubectl", "apply", "-f", CRDPath, "--recursive")
	cmdNs.Stdout = &out
	cmdNs.Stderr = &errB
	//fmt.Printf("\nrunning command %v\n", cmd.String())
	if err := cmdCRD.Run(); err != nil {
		//fmt.Printf("output: %q\n", out.String())
		fmt.Printf("error: %q\n", errB.String())
		return restore, err
	}
	ownerDirInfo, err := ioutil.ReadDir(backupPath + "/owners")
	if err != nil {
		return restore, err
	}
	var returnErr error
	fmt.Printf("\nRestoring owner objects\n")
	for _, gvDir := range ownerDirInfo {
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
		fullPath := filepath.Join(backupPath, "/owners/", gvDir.Name())
		cmd := exec.Command("kubectl", "apply", "-f", fullPath, "--recursive")
		var out, errB bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errB
		//fmt.Printf("\nrunning command %v\n", cmd.String())
		if err := cmd.Run(); err != nil {
			//fmt.Printf("output: %q\n", out.String())
			fmt.Printf("error: %q\n", errB.String())
			returnErr = err
		} else {
			//fmt.Printf("output: %q\n", out.String())
			//fmt.Printf("\ndone!\n")
		}
		defer cmd.Wait()

		// owners created
	}
	if returnErr != nil {
		fmt.Printf("Controller will retry because of error %v", returnErr)
		//return restore, err
	}

	// now create dependents, for each dependent individual kubectl apply, get owner uid label. Get new obj UID and update
	dependentDirInfo, err := ioutil.ReadDir(backupPath + "/dependents")
	if err != nil {
		return restore, err
	}
	fmt.Printf("Now restoring depedents..")
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
			fileBytes, err := ioutil.ReadFile(resourceFileName)
			if err != nil {
				return restore, err
			}
			fileMap := make(map[string]interface{})
			err = json.Unmarshal(fileBytes, &fileMap)
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
				ownerOldUID := ownerRef["uid"].(string)
				dr := h.dynamicClient.Resource(gvr)
				// TODO: which context to use
				ctx := context.Background()
				ownerOldUIDLabel := fmt.Sprintf("%s=%s", common.OldUIDReferenceLabel, ownerOldUID)
				ownerObj, err := dr.List(ctx, k8sv1.ListOptions{LabelSelector: ownerOldUIDLabel})
				if err != nil {
					fmt.Printf("\nerror in listing by label: %v\n", err)
					continue
				}
				newOwnerUID := ownerObj.Items[0].Object["metadata"].(map[string]interface{})["uid"]
				ownerRef["uid"] = newOwnerUID
				ownerRefs[ind] = ownerRef
			}
			metadata["ownerReferences"] = ownerRefs
			metadata["labels"] = map[string]string{"updated": "true"}
			fileMap["metadata"] = metadata
			writeBytes, err := json.Marshal(fileMap)
			if err != nil {
				fmt.Printf("\ndependent json err: %v\n", err)
				//return restore, err
			}
			err = ioutil.WriteFile(resourceFileName, writeBytes, 0777)
			if err != nil {
				fmt.Printf("\nerr writing file: %v\n", err)
				//return restore, err
			}
		}
	}

	// prune
	//filtersBytes, err := ioutil.ReadFile(filepath.Join(backupPath, "filters.json"))
	//if err != nil {
	//	fmt.Printf("\nerr reading file: %v\n", err)
	//	//return restore, err
	//}
	//var backupFilters []v1.BackupFilter
	//if err := json.Unmarshal(filtersBytes, &backupFilters); err != nil {
	//	fmt.Printf("\nerr unmarshaling file: %v\n", err)
	//	//return restore, err
	//}
	//
	//for _, filter := range backupFilters {
	//	groupVersion := filter.ApiGroup
	//	resources := filter.Kinds
	//	// for now, testing with only namespaces
	//	if groupVersion != "v1" {
	//		continue
	//	}
	//	if resources[0] != "namespaces" {
	//		continue
	//	}
	//	res := resources[0]
	//	// evaluate all current ns within given regex
	//	gv, _ := schema.ParseGroupVersion(groupVersion)
	//	gvr := gv.WithResource("namespaces")
	//	var dr dynamic.ResourceInterface
	//	dr = h.dynamicClient.Resource(gvr)
	//	namespacesList, err := dr.List(context.Background(), k8sv1.ListOptions{})
	//	if err != nil {
	//		return restore, err
	//	}
	//	nsBackupPath := filepath.Join(backupPath, "owners", res+"."+gv.Group+"#"+gv.Version)
	//	for _, currNs := range namespacesList.Items {
	//		name := currNs.Object["metadata"].(map[string]interface{})["name"].(string)
	//		nameMatched, err := regexp.MatchString(filter.ResourceNameRegex, name)
	//		if err != nil {
	//			return restore, err
	//		}
	//		if !nameMatched {
	//			continue
	//		}
	//		nsFileName := name + ".json"
	//		// check if file with this name exists in nsbackupPath
	//		_, err = os.Stat(filepath.Join(nsBackupPath, nsFileName))
	//
	//		if os.IsNotExist(err) {
	//			fmt.Printf("\ndeleting ns: %v\n", name)
	//			// not supposed to be here, so delete this ns
	//			//deleteCmd := exec.Command("kubectl", "delete", res, name)
	//			//err := deleteCmd.Run()
	//			//if err != nil {
	//			//	fmt.Printf("\nError deleting ns: %v\n", name)
	//			//}
	//		} else {
	//			fmt.Printf("\nfound ns %v in backup\n", name)
	//		}
	//	}
	//}

	return restore, nil
}
