package restore

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	util "github.com/mrajashree/backup/pkg/controllers"
	backupControllers "github.com/mrajashree/backup/pkg/generated/controllers/backupper.cattle.io/v1"
	"github.com/rancher/wrangler/pkg/slice"
	"github.com/sirupsen/logrus"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

type handler struct {
	ctx                     context.Context
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
		ctx:                     ctx,
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

	backupName := restore.Spec.BackupFileName
	backupPath := filepath.Join(util.BackupBaseDir, backupName)
	// if local, backup tar.gz must be added to the "Local" path
	if restore.Spec.Local != "" {
		backupFilePath := filepath.Join(restore.Spec.Local, backupName)
		if err := util.LoadFromTarGzip(backupFilePath); err != nil {
			return restore, err
		}
	} else if restore.Spec.ObjectStore != nil {
		if err := h.downloadFromS3(restore); err != nil {
			return restore, err
		}
		backupFilePath := filepath.Join(util.BackupBaseDir, backupName)
		if err := util.LoadFromTarGzip(backupFilePath); err != nil {
			return restore, err
		}
	}
	backupPath = strings.TrimSuffix(backupPath, ".tar.gz")

	config, err := h.backupEncryptionConfigs.Get(restore.Spec.EncryptionConfigNamespace, restore.Spec.EncryptionConfigName, k8sv1.GetOptions{})
	if err != nil {
		return restore, err
	}
	transformerMap, err := util.GetEncryptionTransformers(config)
	if err != nil {
		return restore, err
	}
	// first restore namespaces
	if err := h.restoreResource(filepath.Join(backupPath, "namespaces")); err != nil {
		return restore, fmt.Errorf("Error restoring namespace: %v", err)
	}
	// then restore CRDs
	if err := h.restoreResource(filepath.Join(backupPath, "customresourcedefinitions")); err != nil {
		logrus.Errorf("Error restoring CRD: %v", err)
		//return restore, fmt.Errorf("Error restoring CRD: %v", err)
	}

	returnErr := h.restoreOwners(backupPath, transformerMap)
	if returnErr != nil {
		fmt.Printf("Controller will retry because of error %v", returnErr)
		return restore, err
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
				ownerObj, err := dr.List(ctx, k8sv1.ListOptions{LabelSelector: fmt.Sprintf("%s=%s", util.OldUIDReferenceLabel, ownerOldUID)})
				if err != nil {
					return restore, fmt.Errorf("error listing owner by label: %v", err)
				}
				if len(ownerObj.Items) == 0 {
					logrus.Infof("No %v returned for label %v", kind, fmt.Sprintf("%s=%s", util.OldUIDReferenceLabel, ownerOldUID))
					logrus.Errorf("Owner of %v could be a dependent itself, to try this again, return err at end")
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
			// apply doubles data set size because of annotation creation; replace the object; use dynaminClient.Update; kubectl replace of create
			output, err := exec.Command("kubectl", "apply", "-f", resourceFileName).CombinedOutput()
			// use dynamic client.Update && client.UpdateStatus for  subsresources
			// use discovery client to find which resource has status subresource
			if err != nil {
				if strings.Contains(string(output), "--validate=false") {
					logrus.Info("Error during restore, retrying with validate=false")
					retryop, err := exec.Command("kubectl", "apply", "-f", resourceFileName, "--validate=false").CombinedOutput()
					if err != nil {
						return restore, fmt.Errorf("error when restoring %v with validate=false: %v", resourceFileName, string(retryop)+err.Error())
					} else {
						logrus.Info("Retry with validate=false succeeded")
						continue
					}
				}
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
	if err := h.prune(backupPath, restore.Spec.ForcePruneTimeout); err != nil {
		return restore, fmt.Errorf("error pruning during restore")
	}
	logrus.Infof("Done restoring")
	if err := os.RemoveAll(backupPath); err != nil {
		return restore, err
	}
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
		if kind == "customresourcedefinitions.apiextensions.k8s.io" {
			// already restored
			continue
		}
		fmt.Printf("\nrestoring items of gvk %v, %v, %v\n", grp, version, kind)
		if kind == "customresourcedefinitions.apiextensions.k8s.io" {
			continue
		}
		if ok {
			resourceDirPath := filepath.Join(backupPath, "/owners/", gvDir.Name())
			err := decryptAndRestore(resourceDirPath, decryptionTransformer)
			if err != nil {
				return err
			}
			continue
		}
		fullPath := filepath.Join(backupPath, "/owners/", gvDir.Name())
		//h.restoreResource(fullPath)
		output, err := exec.Command("kubectl", "apply", "-f", fullPath, "--recursive").CombinedOutput()
		if err != nil {
			if strings.Contains(string(output), "--validate=false") {
				logrus.Info("Error during restore, retrying with validate=false")
				retryop, err := exec.Command("kubectl", "apply", "-f", fullPath, "--recursive", "--validate=false").CombinedOutput()
				if err != nil {
					return fmt.Errorf("error when restoring %v with validate=false: %v", fullPath, string(retryop)+err.Error())
				} else {
					logrus.Info("Retry with validate=false succeeded")
					continue
				}
			}
			return fmt.Errorf("error restoring secret %v: %v", fullPath, string(output)+err.Error())
		}
	}
	return nil
}

func decryptAndRestore(resourceDirPath string, decryptionTransformer value.Transformer) error {
	resourceDirInfo, err := ioutil.ReadDir(resourceDirPath)
	if err != nil {
		return err
	}
	for _, secretFile := range resourceDirInfo {
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
			return fmt.Errorf("error writing decryped secret %v to file for restore: %v", resourceFileName, err)
		}
		output, err := exec.Command("kubectl", "apply", "-f", resourceFileName).CombinedOutput()
		if err != nil {
			if strings.Contains(string(output), "--validate=false") {
				logrus.Info("Error during restore, retrying with validate=false")
				retryop, err := exec.Command("kubectl", "apply", "-f", resourceFileName, "--validate=false").CombinedOutput()
				if err != nil {
					// write the original encrypted secret before returning
					writeErr := ioutil.WriteFile(resourceFileName, fileBytes, 0777)
					if writeErr != nil {
						return fmt.Errorf("error writing original secret %v to file for restore: %v", resourceFileName, writeErr)
					}
					return fmt.Errorf("error when restoring %v with validate=false: %v", resourceFileName, string(retryop)+err.Error())
				} else {
					logrus.Info("Retry with validate=false succeeded")
				}
			} else if strings.Contains(string(output), "Too long: must have at most 262144 bytes") {
				logrus.Info("Error during restore, retrying with replace")
				retryop, err := exec.Command("kubectl", "replace", "-f", resourceFileName).CombinedOutput()
				if err != nil {
					// retry with kubectl create
					fmt.Printf("string error: %v", string(retryop))
					if strings.Contains(string(retryop), "NotFound") || strings.Contains(string(retryop), "not found") {
						createop, err := exec.Command("kubectl", "create", "-f", resourceFileName).CombinedOutput()
						// write the original encrypted secret before returning
						if err != nil {
							writeErr := ioutil.WriteFile(resourceFileName, fileBytes, 0777)
							if writeErr != nil {
								return fmt.Errorf("error writing original secret %v to file for restore: %v", resourceFileName, writeErr)
							}
							return fmt.Errorf("error when restoring %v with create: %v", resourceFileName, string(createop)+err.Error())
						} else {
							logrus.Info("Retry with kubectl create succeeded")
							continue
						}
					}
					// write the original encrypted secret before returning
					writeErr := ioutil.WriteFile(resourceFileName, fileBytes, 0777)
					if writeErr != nil {
						return fmt.Errorf("error writing original secret %v to file for restore: %v", resourceFileName, writeErr)
					}
					return fmt.Errorf("error when restoring %v with replace: %v", resourceFileName, string(retryop)+err.Error())
				} else {
					logrus.Info("Retry with kubectl replace succeeded")
				}
			} else {
				err = ioutil.WriteFile(resourceFileName, fileBytes, 0777)
				if err != nil {
					return fmt.Errorf("error writing original secret %v to file for restore: %v", resourceFileName, err)
				}
				return fmt.Errorf("error restoring secret %v: %v", resourceFileName, string(output)+err.Error())
			}
		}
		err = ioutil.WriteFile(resourceFileName, fileBytes, 0777)
		if err != nil {
			return fmt.Errorf("error writing original secret %v to file for restore: %v", resourceFileName, err)
		}
	}
	return nil
}

func (h *handler) prune(backupPath string, pruneTimeout int) error {
	// prune
	filtersBytes, err := ioutil.ReadFile(filepath.Join(backupPath, "filters.json"))
	if err != nil {
		return fmt.Errorf("error reading backup fitlers file: %v", err)
	}
	var backupFilters []v1.BackupFilter
	if err := json.Unmarshal(filtersBytes, &backupFilters); err != nil {
		return fmt.Errorf("error unmarshaling backup filters file: %v", err)
	}
	resourceToPrune := make(map[string]map[string]bool)
	resourceToPruneNamespaced := make(map[string]map[string]bool)
	for _, filter := range backupFilters {
		//if !filter.Prune {
		//	continue
		//}
		groupVersion := filter.ApiGroup
		resources := filter.Kinds
		for _, res := range resources {
			var fieldSelector string
			var filteredObjects []unstructured.Unstructured

			gv, _ := schema.ParseGroupVersion(groupVersion)
			gvr := gv.WithResource(res)
			var dr dynamic.ResourceInterface
			dr = h.dynamicClient.Resource(gvr)

			// filter based on namespaces if given
			if len(filter.Namespaces) > 0 {
				for _, ns := range filter.Namespaces {
					fieldSelector += fmt.Sprintf("metadata.namespace=%s,", ns)
				}
			}

			resObjects, err := dr.List(context.Background(), k8sv1.ListOptions{FieldSelector: fieldSelector})
			if err != nil {
				return err
			}

			if filter.NamespaceRegex != "" {
				for _, resObj := range resObjects.Items {
					metadata := resObj.Object["metadata"].(map[string]interface{})
					namespace := metadata["namespace"].(string)
					nsMatched, err := regexp.MatchString(filter.NamespaceRegex, namespace)
					if err != nil {
						return err
					}
					if !nsMatched {
						// resource does not match up to filter, ignore it
						continue
					}
					filteredObjects = append(filteredObjects, resObj)
				}
			} else {
				filteredObjects = resObjects.Items
			}

			for _, obj := range filteredObjects {
				metadata := obj.Object["metadata"].(map[string]interface{})
				name := metadata["name"].(string)
				namespace, _ := metadata["namespace"].(string)
				// resource doesn't match to this filter, so ignore it
				// TODO: check if exact name match logic makes sense
				if len(filter.ResourceNames) > 0 && !slice.ContainsString(filter.ResourceNames, name) {
					continue
				}
				if filter.ResourceNameRegex != "" {
					nameMatched, err := regexp.MatchString(filter.ResourceNameRegex, name)
					if err != nil {
						return err
					}
					// resource doesn't match to this filter, so ignore it
					// for instance for rancher, we want to inlude all p-xxxx ns, so if the ns is totally different, ignore it
					if !nameMatched {
						continue
					}
				}

				// resource matches to this filter, check if it exists in the backup
				// first check if it's a CRD or a namespace
				var backupObjectPaths []string
				for _, path := range []string{"customresourcedefinitions", "namespaces"} {
					objBackupPath := filepath.Join(backupPath, path)
					backupObjectPaths = append(backupObjectPaths, objBackupPath)
				}
				for _, path := range []string{"owners", "dependents"} {
					objBackupPath := filepath.Join(backupPath, path, res+"."+gv.Group+"#"+gv.Version)
					backupObjectPaths = append(backupObjectPaths, objBackupPath)
				}
				exists := false
				for _, path := range backupObjectPaths {
					fileName := name + ".json"
					_, err := os.Stat(filepath.Join(path, fileName))
					if err == nil || os.IsExist(err) {
						exists = true
						// check if this resource was marked for deletion for a previous filter
						if namespace != "" {
							if _, ok := resourceToPruneNamespaced[res][namespace+"/"+name]; ok {
								// remove it from the map of resources to be pruned
								delete(resourceToPruneNamespaced[res], namespace+"/"+name)
							}
						} else {
							if _, ok := resourceToPrune[res][name]; ok {
								delete(resourceToPrune[res], name)
							}
						}
						continue
					}
				}
				if !exists {
					if namespace != "" {
						if _, ok := resourceToPruneNamespaced[res][namespace+"/"+name]; !ok {
							resourceToPruneNamespaced[res] = map[string]bool{namespace + "/" + name: true}
						}

					} else {
						if _, ok := resourceToPrune[res][name]; !ok {
							resourceToPrune[res] = map[string]bool{name: true}
						}
					}
				}
			}
			logrus.Infof("done gathering prune resourceNames for res %v", res)
		}
	}

	fmt.Printf("\nneed to delete following resources: %v\n", resourceToPrune)
	fmt.Printf("\nneed to delete following namespaced resources: %v\n", resourceToPruneNamespaced)
	go func(ctx context.Context, resourceToPrune map[string]map[string]bool, resourceToPruneNamespaced map[string]map[string]bool, pruneTimeout int) {
		// workers group parallel deletion
		deleteResources(resourceToPrune, resourceToPruneNamespaced, false)
		logrus.Infof("Done trying delete -1")
		//time.Sleep(time.Duration(pruneTimeout) * time.Second)
		time.Sleep(2 * time.Second)
		logrus.Infof("Ensuring resources to prune are deleted")
		deleteResources(resourceToPrune, resourceToPruneNamespaced, true)
	}(h.ctx, resourceToPrune, resourceToPruneNamespaced, pruneTimeout)
	return nil
}

func deleteResources(resourceToPrune map[string]map[string]bool, resourceToPruneNamespaced map[string]map[string]bool, removeFinalizers bool) {
	for resource, names := range resourceToPrune {
		for name := range names {
			if removeFinalizers {
				getCMDOp, err := exec.Command("kubectl", "get", resource, name).CombinedOutput()
				if err != nil && strings.Contains(string(getCMDOp), "NotFound") {
					logrus.Infof("Error getting %v, must have been deleted", name)
					continue
				}
				logrus.Infof("Removing finalizer from %v", name)
				removeFinalizerOp, err := exec.Command("kubectl", "patch", resource, name, "-p", `{"metadata":{"finalizers":null}}`).CombinedOutput()
				if err != nil {
					logrus.Errorf("Error removing finalizer on %v: %v", name, string(removeFinalizerOp)+err.Error())
					continue
				}
				logrus.Infof("Removed finalizer from %v", name)
			}
			out, err := exec.Command("kubectl", "delete", resource, name).CombinedOutput()
			if err != nil {
				if strings.Contains(string(out), "NotFound") {
					logrus.Debugf("Resource %v already deleted", name)
					continue
				}
				logrus.Errorf("\nError deleting resource %v: %v\n", name, string(out)+"\n"+err.Error())
			}
		}
	}

	for resource, names := range resourceToPruneNamespaced {
		for nsName := range names {
			split := strings.SplitN(nsName, "/", 2)
			ns := split[0]
			name := split[1]
			if removeFinalizers {
				getCMDOp, err := exec.Command("kubectl", "get", resource, name, "-n", ns).CombinedOutput()
				if err != nil && strings.Contains(string(getCMDOp), "NotFound") {
					logrus.Infof("Error getting %v from namespace %v, must have been deleted", name, ns)
					continue
				}
				logrus.Infof("Removing finalizer from %v", name)
				removeFinalizerOp, err := exec.Command("kubectl", "patch", resource, name, "-p", `{"metadata":{"finalizers":null}}`, "-n", ns).CombinedOutput()
				if err != nil {
					logrus.Errorf("Error removing finalizer on %v: %v", name, string(removeFinalizerOp)+err.Error())
					continue
				}
				logrus.Infof("Removed finalizer from %v", name)
			}
			// not supposed to be here, so delete this resource
			out, err := exec.Command("kubectl", "delete", resource, name, "-n", ns).CombinedOutput()
			if err != nil {
				if strings.Contains(string(out), "NotFound") {
					logrus.Debugf("Resource %v already deleted", name)
					continue
				}
				logrus.Errorf("\nError deleting namespaced resource %v: %v\n", name, string(out)+"\n"+err.Error())
			}
		}
	}
}

func (h *handler) restoreResource(resourcePath string) error {
	output, err := exec.Command("kubectl", "apply", "-f", resourcePath, "--recursive").CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "--validate=false") {
			logrus.Info("Error during restore, retrying with validate=false")
			retryop, err := exec.Command("kubectl", "apply", "-f", resourcePath, "--recursive", "--validate=false").CombinedOutput()
			if err != nil {
				return fmt.Errorf("error when restoring %v with validate=false: %v", resourcePath, string(retryop)+err.Error())
			} else {
				logrus.Info("Retry with validate=false succeeded")
				return nil
			}
		}
		return fmt.Errorf("error restoring resource %v: %v", resourcePath, string(output)+err.Error())
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
