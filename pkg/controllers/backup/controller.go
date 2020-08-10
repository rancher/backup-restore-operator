package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	v1 "github.com/mrajashree/backup/pkg/apis/resources.cattle.io/v1"
	util "github.com/mrajashree/backup/pkg/controllers"
	backupControllers "github.com/mrajashree/backup/pkg/generated/controllers/resources.cattle.io/v1"
	v1core "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/condition"
	"github.com/sirupsen/logrus"

	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

type handler struct {
	ctx             context.Context
	backups         backupControllers.BackupController
	resourceSets    backupControllers.ResourceSetController
	secrets         v1core.SecretController
	namespaces      v1core.NamespaceController
	discoveryClient discovery.DiscoveryInterface
	dynamicClient   dynamic.Interface
}

func Register(
	ctx context.Context,
	backups backupControllers.BackupController,
	backupTemplates backupControllers.ResourceSetController,
	secrets v1core.SecretController,
	namespaces v1core.NamespaceController,
	clientSet *clientset.Clientset,
	dynamicInterface dynamic.Interface) {

	controller := &handler{
		ctx:             ctx,
		backups:         backups,
		resourceSets:    backupTemplates,
		secrets:         secrets,
		namespaces:      namespaces,
		discoveryClient: clientSet.Discovery(),
		dynamicClient:   dynamicInterface,
	}

	// Register handlers
	backups.OnChange(ctx, "backups", controller.OnBackupChange)
}

func (h *handler) OnBackupChange(_ string, backup *v1.Backup) (*v1.Backup, error) {
	if backup.DeletionTimestamp != nil || backup == nil {
		return backup, nil
	}
	if condition.Cond(v1.BackupConditionReady).IsTrue(backup) && condition.Cond(v1.BackupConditionUploaded).IsTrue(backup) {
		if backup.Spec.Schedule == "" {
			// one time backup
			return backup, nil
		}
		// else recurring
		// backup.Schedule = 2hr lastsnapshotTS, same check as the goroutine
		// NO conditions
		// TODO: switch to enqueueAfter instead of cron
		if !condition.Cond(v1.BackupConditionTriggered).IsTrue(backup) {
			// not triggered yet
			return backup, nil
		}
	}

	kubeSystemNS, err := h.namespaces.Get("kube-system", k8sv1.GetOptions{})
	if err != nil {
		return backup, err
	}

	currSnapshotTS := time.Now().Format(time.RFC3339)
	// on OS X writing file with `:` converts colon to forward slash
	currTSForFilename := strings.Replace(currSnapshotTS, ":", "-", -1)
	backupFileName := fmt.Sprintf("%s-%s-%s-%s", backup.Namespace, backup.Name, kubeSystemNS.UID, currTSForFilename)

	// create a temp dir to write all backup files to, delete this before returning
	// empty dir in ioutil.TempDir defaults to os.TempDir
	tmpBackupPath, err := ioutil.TempDir("", backupFileName)
	if err != nil {
		return backup, fmt.Errorf("error creating temp dir: %v", err)
	}
	logrus.Infof("Temporary backup path is %v", tmpBackupPath)

	transformerMap, err := util.GetEncryptionTransformers(backup.Spec.EncryptionConfigName, h.secrets)
	if err != nil {
		removeDirErr := os.RemoveAll(tmpBackupPath)
		if removeDirErr != nil {
			return backup, errors.New(err.Error() + removeDirErr.Error())
		}
		return backup, err
	}

	resourceSetTemplate, err := h.resourceSets.Get(backup.Namespace, backup.Spec.ResourceSetName, k8sv1.GetOptions{})
	if err != nil {
		removeDirErr := os.RemoveAll(tmpBackupPath)
		if removeDirErr != nil {
			return backup, errors.New(err.Error() + removeDirErr.Error())
		}
		return backup, err
	}
	resourceCollectionStartTime := time.Now()
	logrus.Infof("Started gathering resources at %v", resourceCollectionStartTime)
	rh := util.ResourceHandler{
		DiscoveryClient: h.discoveryClient,
		DynamicClient:   h.dynamicClient,
		TransformerMap:  transformerMap,
	}
	resourcesWithStatusSubresource, err := rh.GatherResources(h.ctx, resourceSetTemplate.ResourceSelectors)
	if err != nil {
		removeDirErr := os.RemoveAll(tmpBackupPath)
		if removeDirErr != nil {
			return backup, errors.New(err.Error() + removeDirErr.Error())
		}
		return backup, err
	}
	err = rh.WriteBackupObjects(tmpBackupPath)
	if err != nil {
		removeDirErr := os.RemoveAll(tmpBackupPath)
		if removeDirErr != nil {
			return backup, errors.New(err.Error() + removeDirErr.Error())
		}
		return backup, err
	}
	timeTakenToCollectResources := time.Since(resourceCollectionStartTime)
	logrus.Infof("time taken to collect resources: %v", timeTakenToCollectResources)
	filters, err := json.Marshal(resourceSetTemplate.ResourceSelectors)
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

	subresources, err := json.Marshal(resourcesWithStatusSubresource)
	if err != nil {
		removeDirErr := os.RemoveAll(tmpBackupPath)
		if removeDirErr != nil {
			return backup, errors.New(err.Error() + removeDirErr.Error())
		}
		return backup, err

	}
	err = ioutil.WriteFile(filepath.Join(filtersPath, "statussubresource.json"), subresources, os.ModePerm)
	if err != nil {
		removeDirErr := os.RemoveAll(tmpBackupPath)
		if removeDirErr != nil {
			return backup, errors.New(err.Error() + removeDirErr.Error())
		}
		return backup, err
	}

	condition.Cond(v1.BackupConditionReady).SetStatusBool(backup, true)
	gzipFile := backupFileName + ".tar.gz"
	storageLocation := backup.Spec.StorageLocation
	if storageLocation == nil || storageLocation.Local != "" {
		// for local, to send backup tar to given local path, use that as the path when creating compressed file
		if err := util.CreateTarAndGzip(tmpBackupPath, storageLocation.Local, gzipFile); err != nil {
			removeDirErr := os.RemoveAll(tmpBackupPath)
			if removeDirErr != nil {
				return backup, errors.New(err.Error() + removeDirErr.Error())
			}
			return backup, err
		}
	} else if storageLocation.S3 != nil {
		if err := h.uploadToS3(storageLocation.S3, tmpBackupPath, gzipFile); err != nil {
			removeDirErr := os.RemoveAll(tmpBackupPath)
			if removeDirErr != nil {
				return backup, errors.New(err.Error() + removeDirErr.Error())
			}
			return backup, err
		}
	}
	if err := os.RemoveAll(tmpBackupPath); err != nil {
		return backup, err
	}

	condition.Cond(v1.BackupConditionUploaded).SetStatusBool(backup, true)
	if condition.Cond(v1.BackupConditionTriggered).IsTrue(backup) {
		// not triggered yet
		condition.Cond(v1.BackupConditionTriggered).SetStatusBool(backup, false)
	}

	backup.Status.LastSnapshotTS = currSnapshotTS
	backup.Status.NumSnapshots++
	if updBackup, err := h.backups.UpdateStatus(backup); err != nil {
		return updBackup, err
	}
	logrus.Infof("Done with backup")

	return backup, err
}
