package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	//"regexp"
	//"strings"
	"time"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	resourceController "github.com/rancher/backup-restore-operator/pkg/generated/controllers/resources.cattle.io/v1"
	v1core "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/robfig/cron"
	"github.com/sirupsen/logrus"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	//"github.com/minio/minio-go/v6"
	//"github.com/prometheus/common/log"
)

const EnforceRetentionInterval = "@every 1m"

var recurringSync *backupRetentionSync

type backupRetentionSync struct {
	ctx                   context.Context
	backups               resourceController.BackupController
	namespaces            v1core.NamespaceController
	defaultBackupLocation string
}

func StartBackupRetentionCheckDaemon(ctx context.Context, backups resourceController.BackupController, namespaces v1core.NamespaceController,
	syncRecurringBackupsSchedule, DefaultBackupLocation string) {
	recurringSync = &backupRetentionSync{
		ctx:                   ctx,
		backups:               backups,
		namespaces:            namespaces,
		defaultBackupLocation: DefaultBackupLocation,
	}
	logrus.Infof("in StartBackupRetentionCheckDaemon")
	c := cron.New()
	if syncRecurringBackupsSchedule == "" {
		syncRecurringBackupsSchedule = EnforceRetentionInterval
	}
	schedule, err := cron.ParseStandard(syncRecurringBackupsSchedule)
	if err != nil {
		logrus.Errorf("StartBackupRetentionCheckDaemon: Error parsing cron schedule: %v", err)
		return
	}
	logrus.Infof("in StartBackupRetentionCheckDaemon adding job, schedule: %v", schedule.Next(time.Now()))
	job := cron.FuncJob(syncAllBackups)
	c.Schedule(schedule, job)
	c.Start()
}

func syncAllBackups() {
	if recurringSync == nil {
		return
	}
	logrus.Infof("in syncAllBackups")
	recurringSync.syncBackups()
}

func (b backupRetentionSync) syncBackups() {
	logrus.Infof("Checking backups for deletion based on retention policy")
	backups, err := b.backups.List("", k8sv1.ListOptions{})
	if err != nil {
		logrus.Errorf("syncBackups: Error listing backups: %v", err)
	}
	for _, backup := range backups.Items {
		if backup.Spec.Schedule == "" {
			continue
		}
		err := b.deleteBackupsFollowingRetentionPolicy(&backup)
		if err != nil {
			logrus.Errorf("syncBackups: Error enforcing retention policy on backups for %v: %v", backup.Name, err)
		}
	}
}

func (b backupRetentionSync) deleteBackupsFollowingRetentionPolicy(backup *v1.Backup) error {
	retention := backup.Spec.Retention
	if retention == "" {
		retention = DefaultRetentionTime
	}
	// example, retentionTime = 6 hours
	retentionTime, err := time.ParseDuration(retention)
	if err != nil {
		return err
	}
	logrus.Infof("Retention time for recurring backups for backup %v: %v", backup.Name, retentionTime.Hours())
	// files created 6 hours before now must be deleted
	oldestFileCreationTimeAllowed := time.Now().Add(-retentionTime)
	logrus.Infof("Retaining files created at or after : %v", oldestFileCreationTimeAllowed)

	var backupLocation string
	if backup.Spec.StorageLocation == nil {
		backupLocation = b.defaultBackupLocation
	} else if backup.Spec.StorageLocation.Local != "" {
		backupLocation = backup.Spec.StorageLocation.Local
	} else {
		// TODO s3 deletion
	}
	fileMatchPattern := filepath.Join(backupLocation, fmt.Sprintf("%s-%s*.tar.gz", backup.Namespace, backup.Name))
	logrus.Infof("Finding files starting with %v", fileMatchPattern)
	fileMatches, err := filepath.Glob(fileMatchPattern)
	if err != nil {
		return err
	}
	for _, file := range fileMatches {
		fileInfo, err := os.Stat(file)
		if err != nil {
			logrus.Errorf("Error getting file information for %v: %v", file, err)
			continue
		}
		fileCreationTime := fileInfo.ModTime()
		if fileCreationTime.Before(oldestFileCreationTimeAllowed) {
			logrus.Infof("File %v was created at %v, deleting it to follow retention policy", file, fileCreationTime)
			if err := os.Remove(file); err != nil {
				return err
			}
		}
	}
	return nil
}

//func DeleteS3Backups(backupTime time.Time, retentionPeriod time.Duration, svc *minio.Client, objectStore *v1.S3ObjectStore) {
//	var backupDeleteList []string
//	cutoffTime := backupTime.Add(retentionPeriod * -1)
//
//	// Create a done channel to control 'ListObjectsV2' go routine.
//	doneCh := make(chan struct{})
//
//	// Indicate to our routine to exit cleanly upon return.
//	defer close(doneCh)
//
//	isRecursive := false
//	prefix := ""
//	if len(objectStore.Folder) != 0 {
//		prefix = objectStore.Folder
//		// Recurse will show us the files in the folder
//		isRecursive = true
//	}
//	objectCh := svc.ListObjects(objectStore.BucketName, prefix, isRecursive, doneCh)
//	re := regexp.MustCompile(fmt.Sprintf("%s-%s.+_etcd(|.%s)$", compressedExtension))
//	for object := range objectCh {
//		if object.Err != nil {
//			log.Error("error to fetch s3 file:", object.Err)
//			return
//		}
//		// only parse backup file names that matches *_etcd format
//		if re.MatchString(object.Key) {
//			filename := object.Key
//
//			if len(bc.Folder) != 0 {
//				// example object.Key with folder: folder/timestamp_etcd.zip
//				// folder and separator needs to be stripped so time can be parsed below
//				log.Debugf("Stripping [%s] from [%s]", fmt.Sprintf("%s/", prefix), filename)
//				filename = strings.TrimPrefix(filename, fmt.Sprintf("%s/", prefix))
//			}
//			log.Debugf("object.Key: [%s], filename: [%s]", object.Key, filename)
//
//			backupTime, err := time.Parse(time.RFC3339, strings.Split(filename, "_")[0])
//			if err != nil {
//
//			} else if backupTime.Before(cutoffTime) {
//				// We use object.Key here as we need the full path when a folder is used
//				log.Debugf("Adding [%s] to files to delete, backupTime: [%q], cutoffTime: [%q]", object.Key, backupTime, cutoffTime)
//				backupDeleteList = append(backupDeleteList, object.Key)
//			}
//		}
//	}
//	log.Debugf("Found %d files to delete", len(backupDeleteList))
//
//	for i := range backupDeleteList {
//		log.Infof("Start to delete s3 backup file [%s]", backupDeleteList[i])
//		err := svc.RemoveObject(bc.BucketName, backupDeleteList[i])
//		if err != nil {
//			log.Errorf("Error detected during deletion: %v", err)
//		} else {
//			log.Infof("Success delete s3 backup file [%s]", backupDeleteList[i])
//		}
//	}
//}
