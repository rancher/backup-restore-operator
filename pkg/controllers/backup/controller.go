package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	backupControllers "github.com/rancher/backup-restore-operator/pkg/generated/controllers/resources.cattle.io/v1"
	"github.com/rancher/backup-restore-operator/pkg/monitoring"
	"github.com/rancher/backup-restore-operator/pkg/resourcesets"
	"github.com/rancher/backup-restore-operator/pkg/util"
	"github.com/rancher/backup-restore-operator/pkg/util/encryptionconfig"
	v1core "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"

	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sEncryptionconfig "k8s.io/apiserver/pkg/server/options/encryptionconfig"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/retry"
)

type handler struct {
	ctx                     context.Context
	backups                 backupControllers.BackupController
	resourceSets            backupControllers.ResourceSetController
	secrets                 v1core.SecretController
	namespaces              v1core.NamespaceController
	discoveryClient         discovery.DiscoveryInterface
	dynamicClient           dynamic.Interface
	defaultBackupMountPath  string
	defaultS3BackupLocation *v1.S3ObjectStore
	kubeSystemNS            string
	metricsServerEnabled    bool
	encryptionProviderPath  string
}

func Register(
	ctx context.Context,
	backups backupControllers.BackupController,
	resourceSets backupControllers.ResourceSetController,
	secrets v1core.SecretController,
	namespaces v1core.NamespaceController,
	clientSet *clientset.Clientset,
	dynamicInterface dynamic.Interface,
	defaultLocalBackupLocation string,
	defaultS3 *v1.S3ObjectStore,
	metricsServerEnabled bool,
	encryptionProviderPath string) {

	controller := &handler{
		ctx:                     ctx,
		backups:                 backups,
		resourceSets:            resourceSets,
		secrets:                 secrets,
		namespaces:              namespaces,
		discoveryClient:         clientSet.Discovery(),
		dynamicClient:           dynamicInterface,
		defaultBackupMountPath:  defaultLocalBackupLocation,
		defaultS3BackupLocation: defaultS3,
		metricsServerEnabled:    metricsServerEnabled,
		encryptionProviderPath:  encryptionProviderPath,
	}
	if controller.defaultBackupMountPath != "" {
		logrus.Infof("Default location for storing backups is %v", controller.defaultBackupMountPath)
	} else if controller.defaultS3BackupLocation != nil {
		logrus.Infof("Default s3 location for storing backups is %v", controller.defaultS3BackupLocation)
		logrus.Infof("If credentials are used for default s3, the secret containing creds must exist in chart's namespace %v", util.GetChartNamespace())
	}

	// Use the kube-system NS.UID as the unique ID for a cluster
	kubeSystemNS, err := controller.namespaces.Get("kube-system", k8sv1.GetOptions{})
	if err != nil {
		// fatal log here, because we need the kube-system ns UID while creating any backup file
		logrus.Fatalf("Error getting namespace kube-system %v", err)
	}
	controller.kubeSystemNS = string(kubeSystemNS.UID)
	// Register handlers
	backups.OnChange(ctx, "backups", controller.OnBackupChange)
}

func (h *handler) OnBackupChange(_ string, backup *v1.Backup) (*v1.Backup, error) {
	var err error

	if backup == nil || backup.DeletionTimestamp != nil {
		return backup, nil
	}

	// skips if the backup is singular and already processed
	if backupIsSingularAndComplete(backup) {
		logrus.Debugf("Backup %s has already been processed, skipping it", backup.Name)
		return backup, nil
	}
	logrus.Infof("Processing backup %v", backup.Name)

	validatedBackup := backup.DeepCopy()
	if err = h.setBackupInitialStatus(validatedBackup); err != nil {
		return h.setReconcilingCondition(backup, err)
	}

	if !reflect.DeepEqual(backup, validatedBackup) {
		if validatedBackup, err = h.backups.UpdateStatus(validatedBackup); err != nil {
			return h.setReconcilingCondition(validatedBackup, err)
		}

		return validatedBackup, nil
	}

	if backup.Status.LastSnapshotTS != "" {
		if backup.Status.BackupType == v1.OneTimeBackupType {
			// This means backup CR was updated from recurring to one-time, in which case observedGeneration needs to be updated
			backup.Status.ObservedGeneration = backup.Generation

			logrus.Infof("Updating backup %s from recurring to one-time", backup.Name)
			return h.backups.UpdateStatus(backup)
		}
		if backup.Status.NextSnapshotAt != "" {
			currTime := time.Now().Format(time.RFC3339)
			logrus.Infof("Next snapshot is scheduled for: %v, current time: %v", backup.Status.NextSnapshotAt, currTime)

			nextSnapshotTime, err := time.Parse(time.RFC3339, backup.Status.NextSnapshotAt)
			if err != nil {
				return h.setReconcilingCondition(backup, err)
			}
			if nextSnapshotTime.After(time.Now()) {
				after := nextSnapshotTime.Sub(time.Now())
				h.backups.EnqueueAfter(backup.Name, after)
				if backup.Generation != backup.Status.ObservedGeneration {
					backup.Status.ObservedGeneration = backup.Generation
					return h.backups.UpdateStatus(backup)
				}
				return backup, nil
			}

			// proceed with backup only if current time is same as or after nextSnapshotTime
			logrus.Infof("Processing recurring backup CR %v ", backup.Name)
		}
	}

	if h.metricsServerEnabled {
		backupStartTS := time.Now()
		defer func() {
			backupDoneTS := time.Now()
			monitoring.UpdateTimeSensitiveBackupMetrics(backup.Name, float64(backupDoneTS.Unix()), backupDoneTS.Sub(backupStartTS).Seconds())
			monitoring.UpdateProcessedBackupMetrics(backup.Name, &err)
		}()
	}

	backupFileName, err := h.generateBackupFilename(backup)
	if err != nil {
		return h.setReconcilingCondition(backup, err)
	}
	logrus.Infof("For backup CR %v, filename: %v", backup.Name, backupFileName)

	// create a temp dir to write all backup files to, delete this before returning.
	// empty dir param in os.MkdirTemp. defaults to os.TempDir
	tmpBackupPath, err := os.MkdirTemp("", backupFileName)
	if err != nil {
		return h.setReconcilingCondition(backup, fmt.Errorf("error creating temp dir: %v", err))
	}
	logrus.Infof("Temporary backup path for storing all contents for backup CR %v is %v", backup.Name, tmpBackupPath)

	if err = h.performBackup(backup, tmpBackupPath, backupFileName); err != nil {
		removeDirErr := os.RemoveAll(tmpBackupPath)
		if removeDirErr != nil {
			return h.setReconcilingCondition(backup, errors.New(err.Error()+removeDirErr.Error()))
		}
		return h.setReconcilingCondition(backup, err)
	}

	if err := os.RemoveAll(tmpBackupPath); err != nil {
		return h.setReconcilingCondition(backup, err)
	}
	// check for retention
	var cronSchedule cron.Schedule
	if backup.Spec.Schedule != "" {
		if err := h.deleteBackupsFollowingRetentionPolicy(backup); err != nil {
			return h.setReconcilingCondition(backup, err)
		}
		cronSchedule, err = cron.ParseStandard(backup.Spec.Schedule)
		if err != nil {
			return h.setReconcilingCondition(backup, err)
		}
	}
	storageLocationType := backup.Status.StorageLocation
	updateErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var err error
		backup, err = h.backups.Get(backup.Name, k8sv1.GetOptions{})
		if err != nil {
			return err
		}
		// reset conditions to remove the reconciling condition, because as per kstatus lib its presence is considered an error
		backup.Status.Conditions = []genericcondition.GenericCondition{}

		v1.BackupConditionReady.SetStatusBool(backup, true)
		v1.BackupConditionReady.Message(backup, "Completed")
		v1.BackupConditionUploaded.SetStatusBool(backup, true)

		backup.Status.LastSnapshotTS = time.Now().Format(time.RFC3339)
		if cronSchedule != nil {
			nextBackupAt := cronSchedule.Next(time.Now())
			backup.Status.NextSnapshotAt = nextBackupAt.Format(time.RFC3339)
			after := nextBackupAt.Sub(time.Now())
			h.backups.EnqueueAfter(backup.Name, after)
		}

		backup.Status.ObservedGeneration = backup.Generation
		backup.Status.StorageLocation = storageLocationType
		backup.Status.Filename = backupFileName + ".tar.gz"
		if backup.Spec.EncryptionConfigSecretName != "" {
			backup.Status.Filename += ".enc"
		}
		_, err = h.backups.UpdateStatus(backup)
		return err
	})
	if updateErr != nil {
		return h.setReconcilingCondition(backup, updateErr)
	}

	logrus.Infof("Done with backup")
	return backup, err
}

func (h *handler) performBackup(backup *v1.Backup, tmpBackupPath, backupFileName string) error {
	var err error

	transformerMap := k8sEncryptionconfig.StaticTransformers{}
	if backup.Spec.EncryptionConfigSecretName != "" {
		logrus.Infof("Processing encryption config %v for backup CR %v", backup.Spec.EncryptionConfigSecretName, backup.Name)
		encryptionConfigSecret, err := encryptionconfig.GetEncryptionConfigSecret(h.secrets, backup.Spec.EncryptionConfigSecretName)
		if err != nil {
			return err
		}

		transformerMap, err = encryptionconfig.GetEncryptionTransformersFromSecret(h.ctx, encryptionConfigSecret, h.encryptionProviderPath)
		if err != nil {
			return err
		}
	}

	logrus.Infof("Using resourceSet %v for gathering resources for backup CR %v", backup.Spec.ResourceSetName, backup.Name)
	resourceSetTemplate, err := h.resourceSets.Get(backup.Spec.ResourceSetName, k8sv1.GetOptions{})
	if err != nil {
		return err
	}

	logrus.Infof("Gathering resources for backup CR %v", backup.Name)
	rh := resourcesets.ResourceHandler{
		DiscoveryClient: h.discoveryClient,
		DynamicClient:   h.dynamicClient,
		TransformerMap:  transformerMap,
		Ctx:             h.ctx,
	}
	err = rh.GatherResources(h.ctx, resourceSetTemplate.ResourceSelectors)
	if err != nil {
		return err
	}

	logrus.Infof("Finished gathering resources for backup CR %v, writing to temp location", backup.Name)
	err = rh.WriteBackupObjects(tmpBackupPath)
	if err != nil {
		return err
	}

	logrus.Infof("Saving resourceSet used for backup CR %v", backup.Name)
	filters, err := json.Marshal(resourceSetTemplate)
	if err != nil {
		return err
	}
	filtersPath := filepath.Join(tmpBackupPath, "filters")
	err = os.MkdirAll(filtersPath, os.ModePerm)
	if err != nil {
		return err
	}
	err = os.WriteFile(filepath.Join(filtersPath, "filters.json"), filters, os.ModePerm)
	if err != nil {
		return err
	}

	v1.BackupConditionReady.SetStatusBool(backup, true)

	gzipFile := backupFileName + ".tar.gz"
	if backup.Spec.EncryptionConfigSecretName != "" {
		gzipFile += ".enc"
	}
	storageLocation := backup.Spec.StorageLocation
	if storageLocation == nil {
		logrus.Infof("No storage location specified, checking for default PVC and S3")
		// use the default location that the controller is configured with
		if h.defaultBackupMountPath != "" {
			if err := CreateTarAndGzip(tmpBackupPath, h.defaultBackupMountPath, gzipFile, backup.Name); err != nil {
				return err
			}
			backup.Status.StorageLocation = util.PVBackup
		} else if h.defaultS3BackupLocation != nil {
			// not checking for nil, since if this wasn't provided, the default local location would get used
			if err := h.uploadToS3(backup, h.defaultS3BackupLocation, tmpBackupPath, gzipFile); err != nil {
				return err
			}
			backup.Status.StorageLocation = util.S3Backup
		} else {
			return fmt.Errorf("backup %v needs to specify S3 details, or configure storage location at the operator level", backup.Name)
		}
	} else if storageLocation.S3 != nil {
		backup.Status.StorageLocation = util.S3Backup
		if err := h.uploadToS3(backup, storageLocation.S3, tmpBackupPath, gzipFile); err != nil {
			return err
		}
	}
	return nil
}

func (h *handler) setBackupInitialStatus(backup *v1.Backup) error {
	if backup.Spec.Schedule != "" {
		_, err := cron.ParseStandard(backup.Spec.Schedule)
		if err != nil {
			return fmt.Errorf("error parsing invalid cron string for schedule: %v", err)
		}

		backup.Status.BackupType = v1.RecurringBackupType
	} else {
		backup.Status.BackupType = v1.OneTimeBackupType
	}

	return nil
}

func (h *handler) generateBackupFilename(backup *v1.Backup) (string, error) {
	currSnapshotTS := time.Now().Format(time.RFC3339)
	// on OS X writing file with `:` converts colon to forward slash
	currTSForFilename := strings.Replace(currSnapshotTS, ":", "-", -1)
	backupFileName := fmt.Sprintf("%s-%s-%s", backup.Name, h.kubeSystemNS, currTSForFilename)
	return backupFileName, nil
}

// https://github.com/kubernetes-sigs/cli-utils/tree/master/pkg/kstatus
// Reconciling and Stalled conditions are present and with a value of true whenever something unusual happens.
func (h *handler) setReconcilingCondition(backup *v1.Backup, originalErr error) (*v1.Backup, error) {
	if !v1.BackupConditionReconciling.IsUnknown(backup) && v1.BackupConditionReconciling.GetReason(backup) == "Error" {
		reconcileMsg := v1.BackupConditionReconciling.GetMessage(backup)
		if strings.Contains(reconcileMsg, originalErr.Error()) {
			// no need to update object status again, because if another UpdateStatus is called without needing it, controller will
			// process the same object immediately without its default backoff
			return backup, originalErr
		}
	}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var err error
		updBackup, err := h.backups.Get(backup.Name, k8sv1.GetOptions{})
		if err != nil {
			return err
		}

		v1.BackupConditionReconciling.SetStatusBool(updBackup, true)
		v1.BackupConditionReconciling.SetError(updBackup, "", originalErr)
		v1.BackupConditionReady.Message(updBackup, "Retrying")

		_, err = h.backups.UpdateStatus(updBackup)
		return err
	})
	if err != nil {
		return backup, errors.New(originalErr.Error() + err.Error())
	}
	return backup, originalErr
}

// backupIsSingularAndComplete checks if the backup is a one-time backup and has not been modified
func backupIsSingularAndComplete(backup *v1.Backup) bool {
	return backup.Status.BackupType == v1.OneTimeBackupType && backup.Generation == backup.Status.ObservedGeneration
}
