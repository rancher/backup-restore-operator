package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	util "github.com/mrajashree/backup/pkg/controllers"
	backupControllers "github.com/mrajashree/backup/pkg/generated/controllers/backupper.cattle.io/v1"
	"github.com/rancher/wrangler/pkg/condition"
	"github.com/sirupsen/logrus"
	"io/ioutil"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"os"
	"path/filepath"
	"time"
)

type handler struct {
	ctx                     context.Context
	backups                 backupControllers.BackupController
	backupTemplates         backupControllers.BackupTemplateController
	backupEncryptionConfigs backupControllers.BackupEncryptionConfigController
	discoveryClient         discovery.DiscoveryInterface
	dynamicClient           dynamic.Interface
}

func Register(
	ctx context.Context,
	backups backupControllers.BackupController,
	backupTemplates backupControllers.BackupTemplateController,
	backupEncryptionConfigs backupControllers.BackupEncryptionConfigController,
	clientSet *clientset.Clientset,
	dynamicInterface dynamic.Interface) {

	controller := &handler{
		ctx:                     ctx,
		backups:                 backups,
		backupTemplates:         backupTemplates,
		backupEncryptionConfigs: backupEncryptionConfigs,
		discoveryClient:         clientSet.Discovery(),
		dynamicClient:           dynamicInterface,
	}

	// Register handlers
	backups.OnChange(ctx, "backups", controller.OnBackupChange)
}

func (h *handler) OnBackupChange(_ string, backup *v1.Backup) (*v1.Backup, error) {
	if backup.DeletionTimestamp != nil || backup == nil {
		return backup, nil
	}
	if condition.Cond(v1.BackupConditionReady).IsTrue(backup) && condition.Cond(v1.BackupConditionUploaded).IsTrue(backup) {
		return backup, nil
	}
	// empty dir defaults to os.TempDir
	tmpBackupPath, err := ioutil.TempDir("", backup.Spec.BackupFileName)
	if err != nil {
		return backup, fmt.Errorf("error creating temp dir: %v", err)
	}
	logrus.Infof("Temporary backup path is %v", tmpBackupPath)
	//h.discoveryClient.ServerGroupsAndResources()
	config, err := h.backupEncryptionConfigs.Get(backup.Spec.EncryptionConfigNamespace, backup.Spec.EncryptionConfigName, k8sv1.GetOptions{})
	if err != nil {
		removeDirErr := os.RemoveAll(tmpBackupPath)
		if removeDirErr != nil {
			return backup, errors.New(err.Error() + removeDirErr.Error())
		}
		return backup, err
	}
	transformerMap, err := util.GetEncryptionTransformers(config)
	if err != nil {
		removeDirErr := os.RemoveAll(tmpBackupPath)
		if removeDirErr != nil {
			return backup, errors.New(err.Error() + removeDirErr.Error())
		}
		return backup, err
	}

	template, err := h.backupTemplates.Get("default", backup.Spec.BackupTemplate, k8sv1.GetOptions{})
	if err != nil {
		removeDirErr := os.RemoveAll(tmpBackupPath)
		if removeDirErr != nil {
			return backup, errors.New(err.Error() + removeDirErr.Error())
		}
		return backup, err
	}
	resourceCollectionStartTime := time.Now()
	logrus.Infof("Started gathering resources at %v", resourceCollectionStartTime)
	err = h.gatherResources(template.BackupFilters, tmpBackupPath, transformerMap)
	if err != nil {
		removeDirErr := os.RemoveAll(tmpBackupPath)
		if removeDirErr != nil {
			return backup, errors.New(err.Error() + removeDirErr.Error())
		}
		return backup, err
	}
	timeTakenToCollectResources := time.Since(resourceCollectionStartTime)
	logrus.Infof("time taken to collect resources: %v", timeTakenToCollectResources)
	filters, err := json.Marshal(template.BackupFilters)
	if err != nil {
		removeDirErr := os.RemoveAll(tmpBackupPath)
		if removeDirErr != nil {
			return backup, errors.New(err.Error() + removeDirErr.Error())
		}
		return backup, err
	}
	filtersPath := filepath.Join(tmpBackupPath, "filters")
	err = os.Mkdir(filtersPath, os.ModePerm)
	if err != nil {
		removeDirErr := os.RemoveAll(tmpBackupPath)
		if removeDirErr != nil {
			return backup, errors.New(err.Error() + removeDirErr.Error())
		}
	}
	err = ioutil.WriteFile(filepath.Join(filtersPath, "filters.json"), filters, os.ModePerm)
	if err != nil {
		removeDirErr := os.RemoveAll(tmpBackupPath)
		if removeDirErr != nil {
			return backup, errors.New(err.Error() + removeDirErr.Error())
		}
		return backup, err
	}

	condition.Cond(v1.BackupConditionReady).SetStatusBool(backup, true)
	gzipFile := backup.Spec.BackupFileName + ".tar.gz"
	if backup.Spec.Local != "" {
		// for local, to send backup tar to given local path, use that as the path when creating compressed file
		if err := util.CreateTarAndGzip(tmpBackupPath, backup.Spec.Local, gzipFile); err != nil {
			removeDirErr := os.RemoveAll(tmpBackupPath)
			if removeDirErr != nil {
				return backup, errors.New(err.Error() + removeDirErr.Error())
			}
			return backup, err
		}
	} else if backup.Spec.ObjectStore != nil {
		if err := h.uploadToS3(backup, tmpBackupPath, gzipFile); err != nil {
			removeDirErr := os.RemoveAll(tmpBackupPath)
			if removeDirErr != nil {
				return backup, errors.New(err.Error() + removeDirErr.Error())
			}
			return backup, err
		}
	}
	condition.Cond(v1.BackupConditionUploaded).SetStatusBool(backup, true)
	if err := os.RemoveAll(tmpBackupPath); err != nil {
		return backup, err
	}
	if updBackup, err := h.backups.UpdateStatus(backup); err != nil {
		return updBackup, err
	}
	logrus.Infof("Done with backup")

	return backup, err
}

func (h *handler) writeBackupObjects(resObjects []unstructured.Unstructured, res k8sv1.APIResource, gv schema.GroupVersion, backupPath string, transformerMap map[schema.GroupResource]value.Transformer) error {
	for _, resObj := range resObjects {
		metadata := resObj.Object["metadata"].(map[string]interface{})
		// if an object has deletiontimestamp and finalizers, back it up. If there are no finalizers, ignore
		if _, deletionTs := metadata["deletionTimestamp"]; deletionTs {
			if _, finSet := metadata["finalizers"]; !finSet {
				// no finalizers set, don't backup object
				continue
			}
		}

		currObjLabels := metadata["labels"]
		objName := metadata["name"].(string)
		if resObj.Object["metadata"].(map[string]interface{})["uid"] != nil {
			oidLabel := map[string]string{util.OldUIDReferenceLabel: resObj.Object["metadata"].(map[string]interface{})["uid"].(string)}
			if currObjLabels == nil {
				metadata["labels"] = oidLabel
			} else {
				currLabels := currObjLabels.(map[string]interface{})
				currLabels[util.OldUIDReferenceLabel] = resObj.Object["metadata"].(map[string]interface{})["uid"].(string)
				metadata["labels"] = currLabels
			}
		}

		// TODO: decide whether to store deletionTimestamp or not
		// TOTO:generation
		for _, field := range []string{"uid", "creationTimestamp", "selfLink", "resourceVersion"} {
			delete(metadata, field)
		}

		gr := schema.ParseGroupResource(res.Name + "." + res.Group)
		encryptionTransformer := transformerMap[gr]
		additionalAuthenticatedData := objName
		//if res.Namespaced {
		//	additionalAuthenticatedData = metadata["namespace"].(string) + "/" + additionalAuthenticatedData
		//}

		resourcePath := backupPath + "/" + res.Name + "." + gv.Group + "#" + gv.Version
		if err := createResourceDir(resourcePath); err != nil {
			return err
		}

		err := writeToBackup(resObj.Object, resourcePath, objName, encryptionTransformer, additionalAuthenticatedData)
		if err != nil {
			return err
		}
	}
	return nil
}

func skipBackup(res k8sv1.APIResource) bool {
	if !canListResource(res.Verbs) {
		logrus.Debugf("Cannot list resource %v, not backing up", res)
		return true
	}
	if !canUpdateResource(res.Verbs) {
		logrus.Debugf("Cannot update resource %v, not backing up\n", res)
		return true
	}
	return false
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

func writeToBackup(resource map[string]interface{}, backupPath, filename string, transformer value.Transformer, additionalAuthenticatedData string) error {
	f, err := os.Create(filepath.Join(backupPath, filepath.Base(filename+".json")))
	if err != nil {
		return fmt.Errorf("error creating temp file: %v", err)
	}
	defer f.Close()

	resourceBytes, err := json.Marshal(resource)
	if err != nil {
		return fmt.Errorf("error converting resource to JSON: %v", err)
	}
	if transformer != nil {
		encrypted, err := transformer.TransformToStorage(resourceBytes, value.DefaultContext([]byte(additionalAuthenticatedData)))
		if err != nil {
			return fmt.Errorf("error converting resource to JSON: %v", err)
		}
		resourceBytes, err = json.Marshal(encrypted)
		if err != nil {
			return fmt.Errorf("error converting encrypted resource to JSON: %v", err)
		}
	}
	if _, err := f.Write(resourceBytes); err != nil {
		return fmt.Errorf("error writing JSON to file: %v", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("error closing file: %v", err)
	}
	return nil
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
