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

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	backupControllers "github.com/rancher/backup-restore-operator/pkg/generated/controllers/resources.cattle.io/v1"
	"github.com/rancher/backup-restore-operator/pkg/resourcesets"
	"github.com/rancher/backup-restore-operator/pkg/util"
	v1core "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/pkg/condition"
	"github.com/robfig/cron"
	"github.com/sirupsen/logrus"

	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

type handler struct {
	ctx                   context.Context
	backups               backupControllers.BackupController
	resourceSets          backupControllers.ResourceSetController
	secrets               v1core.SecretController
	namespaces            v1core.NamespaceController
	discoveryClient       discovery.DiscoveryInterface
	dynamicClient         dynamic.Interface
	defaultBackupLocation string
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
	defaultLocation, err := ioutil.TempDir("", "defaultbackuplocation")
	if err != nil {
		logrus.Errorf("Error setting default location")
	}
	controller.defaultBackupLocation = defaultLocation
	logrus.Infof("Default location for storing backups is %v", defaultLocation)
	// Register handlers
	backups.OnChange(ctx, "backups", controller.OnBackupChange)
}

func (h *handler) OnBackupChange(_ string, backup *v1.Backup) (*v1.Backup, error) {
	if backup == nil || backup.DeletionTimestamp != nil {
		return backup, nil
	}
	//if condition.Cond(v1.BackupConditionReady).IsTrue(backup) && condition.Cond(v1.BackupConditionUploaded).IsTrue(backup) {
	if backup.Status.LastSnapshotTS != "" {
		if backup.Spec.Schedule == "" {
			// one time backup
			return backup, nil
		}
		currTime := time.Now().Round(time.Minute).Format(time.RFC3339)
		fmt.Printf("backup.Status.NextSnapshotAt: %v, time.Now(): %v", backup.Status.NextSnapshotAt, currTime)

		if backup.Status.NextSnapshotAt != "" {
			nextSnapshotTime, err := time.Parse(time.RFC3339, backup.Status.NextSnapshotAt)
			if err != nil {
				return backup, err
			}
			if nextSnapshotTime.After(time.Now().Round(time.Minute)) {
				return backup, nil
			}
		}
	}

	kubeSystemNS, err := h.namespaces.Get("kube-system", k8sv1.GetOptions{})
	if err != nil {
		return backup, err
	}

	currSnapshotTS := time.Now().Format(time.RFC3339)
	// on OS X writing file with `:` converts colon to forward slash
	currTSForFilename := strings.Replace(currSnapshotTS, ":", "#", -1)
	backupFileName := fmt.Sprintf("%s-%s-%s-%s", backup.Namespace, backup.Name, kubeSystemNS.UID, currTSForFilename)

	// create a temp dir to write all backup files to, delete this before returning
	// empty dir in ioutil.TempDir defaults to os.TempDir
	tmpBackupPath, err := ioutil.TempDir("", backupFileName)
	if err != nil {
		return backup, fmt.Errorf("error creating temp dir: %v", err)
	}
	logrus.Infof("Temporary backup path is %v", tmpBackupPath)

	transformerMap := make(map[schema.GroupResource]value.Transformer)
	if backup.Spec.EncryptionConfigName != "" {
		transformerMap, err = util.GetEncryptionTransformers(backup.Spec.EncryptionConfigName, h.secrets)
		if err != nil {
			removeDirErr := os.RemoveAll(tmpBackupPath)
			if removeDirErr != nil {
				return backup, errors.New(err.Error() + removeDirErr.Error())
			}
			return backup, err
		}
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
	rh := resourcesets.ResourceHandler{
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
	if storageLocation == nil {
		if err := CreateTarAndGzip(tmpBackupPath, h.defaultBackupLocation, gzipFile); err != nil {
			removeDirErr := os.RemoveAll(tmpBackupPath)
			if removeDirErr != nil {
				return backup, errors.New(err.Error() + removeDirErr.Error())
			}
			return backup, err
		}
	} else if storageLocation.Local != "" {
		// for local, to send backup tar to given local path, use that as the path when creating compressed file
		if err := CreateTarAndGzip(tmpBackupPath, storageLocation.Local, gzipFile); err != nil {
			removeDirErr := os.RemoveAll(tmpBackupPath)
			if removeDirErr != nil {
				return backup, errors.New(err.Error() + removeDirErr.Error())
			}
			return backup, err
		}
	} else if storageLocation.S3 != nil {
		if err := h.uploadToS3(backup.Namespace, storageLocation.S3, tmpBackupPath, gzipFile); err != nil {
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

	backup.Status.LastSnapshotTS = currSnapshotTS
	backup.Status.NumSnapshots++
	if backup.Spec.Schedule != "" {
		cronSchedule, err := cron.ParseStandard(backup.Spec.Schedule)
		if err != nil {
			return backup, err
		}
		nextBackupAt := cronSchedule.Next(time.Now()).Round(time.Minute)
		backup.Status.NextSnapshotAt = nextBackupAt.Format(time.RFC3339)
		after := nextBackupAt.Sub(time.Now().Round(time.Minute))
		h.backups.EnqueueAfter(backup.Namespace, backup.Name, after)
		//maxBackupsCount := backup.Spec.Retention
		//if backup.Status.NumSnapshots > backup.Spec.Retention {
		//	snapshotsToRemove
		//}
	}

	if updBackup, err := h.backups.UpdateStatus(backup); err != nil {
		return updBackup, err
	}
	logrus.Infof("Done with backup")

	return backup, err
}
