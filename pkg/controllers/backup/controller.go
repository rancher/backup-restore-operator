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

const DefaultRetentionTime = "6h"

type handler struct {
	ctx                   context.Context
	backups               backupControllers.BackupController
	resourceSets          backupControllers.ResourceSetController
	secrets               v1core.SecretController
	namespaces            v1core.NamespaceController
	discoveryClient       discovery.DiscoveryInterface
	dynamicClient         dynamic.Interface
	defaultBackupLocation string
	kubeSystemNS          string
}

func Register(
	ctx context.Context,
	backups backupControllers.BackupController,
	backupTemplates backupControllers.ResourceSetController,
	secrets v1core.SecretController,
	namespaces v1core.NamespaceController,
	clientSet *clientset.Clientset,
	dynamicInterface dynamic.Interface,
	defaultBackupLocation string) {

	controller := &handler{
		ctx:                   ctx,
		backups:               backups,
		resourceSets:          backupTemplates,
		secrets:               secrets,
		namespaces:            namespaces,
		discoveryClient:       clientSet.Discovery(),
		dynamicClient:         dynamicInterface,
		defaultBackupLocation: defaultBackupLocation,
	}

	logrus.Infof("Default location for storing backups is %v", controller.defaultBackupLocation)
	kubeSystemNS, err := controller.namespaces.Get("kube-system", k8sv1.GetOptions{})
	if err != nil {
		logrus.Fatal("Error getting namespace kube-system %v", err)
	}
	controller.kubeSystemNS = string(kubeSystemNS.UID)
	// Register handlers
	backups.OnChange(ctx, "backups", controller.OnBackupChange)
}

func (h *handler) OnBackupChange(_ string, backup *v1.Backup) (*v1.Backup, error) {
	if backup == nil || backup.DeletionTimestamp != nil {
		return backup, nil
	}
	if backup.Status.LastSnapshotTS != "" {
		if backup.Spec.Schedule == "" {
			// one time backup
			return backup, nil
		}
		currTime := time.Now().Round(time.Minute).Format(time.RFC3339)
		logrus.Infof("Next snapshot is scheduled for: %v, current time: %v", backup.Status.NextSnapshotAt, currTime)
		if backup.Status.NextSnapshotAt != "" {
			nextSnapshotTime, err := time.Parse(time.RFC3339, backup.Status.NextSnapshotAt)
			if err != nil {
				return h.setReconcilingCondition(backup, err)
			}
			if nextSnapshotTime.After(time.Now().Round(time.Minute)) {
				return backup, nil
			}
		}
	}

	backupFileName, err := h.generateBackupFilename(backup)
	if err != nil {
		return h.setReconcilingCondition(backup, err)
	}

	// create a temp dir to write all backup files to, delete this before returning
	// empty dir in ioutil.TempDir defaults to os.TempDir
	tmpBackupPath, err := ioutil.TempDir("", backupFileName)
	if err != nil {
		return h.setReconcilingCondition(backup, fmt.Errorf("error creating temp dir: %v", err))
	}
	logrus.Infof("Temporary backup path is %v", tmpBackupPath)

	if err := h.performBackup(backup, tmpBackupPath, backupFileName); err != nil {
		removeDirErr := os.RemoveAll(tmpBackupPath)
		if removeDirErr != nil {
			return h.setReconcilingCondition(backup, errors.New(err.Error()+removeDirErr.Error()))
		}
		return backup, err
	}

	if err := os.RemoveAll(tmpBackupPath); err != nil {
		return h.setReconcilingCondition(backup, err)
	}

	condition.Cond(v1.BackupConditionUploaded).SetStatusBool(backup, true)

	backup.Status.LastSnapshotTS = time.Now().Format(time.RFC3339)
	backup.Status.NumSnapshots++
	if backup.Spec.Schedule != "" {
		cronSchedule, err := cron.ParseStandard(backup.Spec.Schedule)
		if err != nil {
			return h.setReconcilingCondition(backup, err)
		}
		nextBackupAt := cronSchedule.Next(time.Now()).Round(time.Minute)
		backup.Status.NextSnapshotAt = nextBackupAt.Format(time.RFC3339)
		after := nextBackupAt.Sub(time.Now().Round(time.Minute))
		h.backups.EnqueueAfter(backup.Namespace, backup.Name, after)
	}
	backup.Status.ObservedGeneration = backup.Generation
	if updBackup, err := h.backups.UpdateStatus(backup); err != nil {
		return h.setReconcilingCondition(updBackup, err)
	}
	logrus.Infof("Done with backup")

	return backup, err
}

func (h *handler) performBackup(backup *v1.Backup, tmpBackupPath, backupFileName string) error {
	var err error
	transformerMap := make(map[schema.GroupResource]value.Transformer)
	if backup.Spec.EncryptionConfigName != "" {
		transformerMap, err = util.GetEncryptionTransformers(backup.Spec.EncryptionConfigName, h.secrets)
		if err != nil {
			return err
		}
	}

	resourceSetTemplate, err := h.resourceSets.Get(backup.Namespace, backup.Spec.ResourceSetName, k8sv1.GetOptions{})
	if err != nil {
		return err
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
		return err
	}

	err = rh.WriteBackupObjects(tmpBackupPath)
	if err != nil {
		return err
	}
	timeTakenToCollectResources := time.Since(resourceCollectionStartTime)
	logrus.Infof("time taken to collect resources: %v", timeTakenToCollectResources)
	filters, err := json.Marshal(resourceSetTemplate.ResourceSelectors)
	if err != nil {
		return err
	}
	filtersPath := filepath.Join(tmpBackupPath, "filters")
	err = os.Mkdir(filtersPath, os.ModePerm)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(filepath.Join(filtersPath, "filters.json"), filters, os.ModePerm)
	if err != nil {
		return err
	}
	subresources, err := json.Marshal(resourcesWithStatusSubresource)
	if err != nil {
		return err

	}
	err = ioutil.WriteFile(filepath.Join(filtersPath, "statussubresource.json"), subresources, os.ModePerm)
	if err != nil {
		return err
	}

	condition.Cond(v1.BackupConditionReady).SetStatusBool(backup, true)

	gzipFile := backupFileName + ".tar.gz"
	storageLocation := backup.Spec.StorageLocation
	if storageLocation == nil {
		if err := CreateTarAndGzip(tmpBackupPath, h.defaultBackupLocation, gzipFile); err != nil {
			return err
		}
	} else if storageLocation.Local != "" {
		// for local, to send backup tar to given local path, use that as the path when creating compressed file
		if err := CreateTarAndGzip(tmpBackupPath, storageLocation.Local, gzipFile); err != nil {
			return err
		}
	} else if storageLocation.S3 != nil {
		if err := h.uploadToS3(backup.Namespace, storageLocation.S3, tmpBackupPath, gzipFile); err != nil {
			return err
		}
	}
	return nil
}

func (h *handler) generateBackupFilename(backup *v1.Backup) (string, error) {
	currSnapshotTS := time.Now().Format(time.RFC3339)
	// on OS X writing file with `:` converts colon to forward slash
	currTSForFilename := strings.Replace(currSnapshotTS, ":", "#", -1)
	backupFileName := fmt.Sprintf("%s-%s-%s-%s", backup.Namespace, backup.Name, h.kubeSystemNS, currTSForFilename)
	return backupFileName, nil
}

// https://github.com/kubernetes-sigs/cli-utils/tree/master/pkg/kstatus
// Reconciling and Stalled conditions are present and with a value of true whenever something unusual happens.
func (h *handler) setReconcilingCondition(backup *v1.Backup, originalErr error) (*v1.Backup, error) {
	condition.Cond(v1.BackupConditionReconciling).SetStatusBool(backup, true)
	if updBackup, err := h.backups.UpdateStatus(backup); err != nil {
		return updBackup, errors.New(originalErr.Error() + err.Error())
	}
	return backup, originalErr
}
