package restore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os/exec"
	"path/filepath"
	"strings"

	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	common "github.com/mrajashree/backup/pkg/controllers"
	backupControllers "github.com/mrajashree/backup/pkg/generated/controllers/backupper.cattle.io/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

type handler struct {
	restores                backupControllers.RestoreController
	backups                 backupControllers.BackupController
	backupEncryptionConfigs backupControllers.BackupEncryptionConfigController
	discoveryClient         discovery.DiscoveryInterface
	dynamicClient           dynamic.Interface
}

func Register(
	ctx context.Context,
	restores backupControllers.RestoreController,
	backups backupControllers.BackupController,
	backupEncryptionConfigs backupControllers.BackupEncryptionConfigController,
	clientSet *clientset.Clientset,
	dynamicInterface dynamic.Interface) {

	controller := &handler{
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
	backupName := restore.Spec.BackupName
	backup, err := h.backups.Get("default", backupName, k8sv1.GetOptions{})
	if err != nil {
		return restore, err
	}
	// TODO: logic to read from object store
	backupPath := backup.Spec.Local
	config, err := h.backupEncryptionConfigs.Get(restore.Spec.BackupEncryptionConfigNamespace, restore.Spec.BackupEncryptionConfigName, k8sv1.GetOptions{})
	if err != nil {
		return restore, err
	}
	transformerMap, err := common.GetEncryptionTransformers(config)
	if err != nil {
		return restore, err
	}
	// first restore namespaces
	if err := h.restoreResource(filepath.Join(backupPath, "namespaces")); err != nil {
		return restore, err
	}
	// then restore CRDs
	if err := h.restoreResource(filepath.Join(backupPath, "customresourcedefinitions")); err != nil {
		return restore, err
	}

	returnErr := h.restoreOwners(backupPath, transformerMap)
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
				ownerObj, err := dr.List(ctx, k8sv1.ListOptions{LabelSelector: fmt.Sprintf("%s=%s", common.OldUIDReferenceLabel, ownerOldUID)})
				if err != nil {
					return restore, fmt.Errorf("error listing owner by label: %v", err)
				}
				if len(ownerObj.Items) == 0 {
					fmt.Printf("\nNEWERR3 owner could be a dependent itself, to try this again, return err at end\n")
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
			output, err := exec.Command("kubectl", "apply", "-f", resourceFileName).CombinedOutput()
			if err != nil {
				fmt.Printf("\noutput: %v, err : %v\n", string(output), err)
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

func (h *handler) restoreOwners(backupPath string, transformerMap map[schema.GroupResource]value.Transformer) error {
	ownerDirInfo, err := ioutil.ReadDir(backupPath + "/owners")
	if err != nil {
		return err
	}
	for _, gvDir := range ownerDirInfo {
		gvkStr := gvDir.Name()
		gvkParts := strings.Split(gvkStr, "#")
		version := gvkParts[1]
		kindGrp := strings.SplitN(gvkParts[0], ".", 1)
		kind := strings.TrimRight(kindGrp[0], ".")
		var grp string
		if len(kindGrp) > 1 {
			grp = kindGrp[1]
		}
		gr := schema.ParseGroupResource(kind + "." + grp)
		decryptionTransformer, ok := transformerMap[gr]
		fmt.Printf("\nrestoring items of gvk %v, %v, %v\n", grp, version, kind)
		if ok {
			resourceDirPath := filepath.Join(backupPath, "/owners/", gvDir.Name())
			err := decryptAndRestore(resourceDirPath, decryptionTransformer)
			if err != nil {
				return err
			}
			continue
		}

		fullPath := filepath.Join(backupPath, "/owners/", gvDir.Name())
		cmd := exec.Command("kubectl", "apply", "-f", fullPath, "--recursive")
		var out, errB bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errB
		if err := cmd.Run(); err != nil {
			fmt.Printf("error: %q\n", errB.String())
			//return err
		}
		defer cmd.Wait()

		// owners created
	}
	return nil
}

func decryptAndRestore(resourceDirPath string, decryptionTransformer value.Transformer) error {
	resourceDirInfo, err := ioutil.ReadDir(resourceDirPath)
	if err != nil {
		return err
	}
	for _, secretFile := range resourceDirInfo {
		if secretFile.Name() == "c-c-dpqd6.json" {
			continue
		}
		resourceFileName := filepath.Join(resourceDirPath, secretFile.Name())
		// read file and decrypt
		fileBytes, err := ioutil.ReadFile(resourceFileName)
		if err != nil {
			return err
		}
		var encryptedBytes []byte
		if err := json.Unmarshal(fileBytes, &encryptedBytes); err != nil {
			return err
		}
		decrypted, _, err := decryptionTransformer.TransformFromStorage(encryptedBytes, value.DefaultContext(secretFile.Name()))
		if err != nil {
			return err
		}
		// write secret to same file to apply. then write fileBytes again
		err = ioutil.WriteFile(resourceFileName, decrypted, 0777)
		if err != nil {
			return err
		}
		output, err := exec.Command("kubectl", "apply", "-f", resourceFileName).CombinedOutput()
		if err != nil {
			fmt.Printf("\noutput: %v, err : %v\n", output, err)
			return err
		}
		err = ioutil.WriteFile(resourceFileName, fileBytes, 0777)
		if err != nil {
			return err
		}
	}
	return nil
}

func (h *handler) restoreResource(resourcePath string) error {
	cmd := exec.Command("kubectl", "apply", "-f", resourcePath, "--recursive")
	var out, errB bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errB
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error restoring resource %v: %v", resourcePath, errB.String())
	}
	return nil
}

//cmd := fmt.Sprintf("cat <<EOF | kubectl apply -f - `%s` EOF", string(decrypted))
//stdOut, stdErr := exec.Command("bash", "-c", cmd).CombinedOutput()
//if stdErr != nil {
//	fmt.Printf("\nout: %v\n", string(stdOut))
//	fmt.Printf("secret apply error %v in executing command", stdErr.Error())
//	return err
//}
//fmt.Printf("\nout: %v\n", string(stdOut))
