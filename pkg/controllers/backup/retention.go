package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/minio/minio-go/v6"
	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	resourceController "github.com/rancher/backup-restore-operator/pkg/generated/controllers/resources.cattle.io/v1"
	"github.com/rancher/backup-restore-operator/pkg/objectstore"
	v1core "github.com/rancher/wrangler-api/pkg/generated/controllers/core/v1"
	"github.com/robfig/cron"
	"github.com/sirupsen/logrus"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
)

const EnforceRetentionInterval = "@every 5m"

var recurringSync *backupRetentionSync

type backupRetentionSync struct {
	ctx                   context.Context
	backups               resourceController.BackupController
	namespaces            v1core.NamespaceController
	dynamicClient         dynamic.Interface
	defaultBackupLocation string
	kubeSystemNS          string
}

func StartBackupRetentionCheckDaemon(ctx context.Context, backups resourceController.BackupController,
	namespaces v1core.NamespaceController,
	dynamicInterface dynamic.Interface,
	retentionSchedule, DefaultBackupLocation string) {
	recurringSync = &backupRetentionSync{
		ctx:                   ctx,
		backups:               backups,
		namespaces:            namespaces,
		dynamicClient:         dynamicInterface,
		defaultBackupLocation: DefaultBackupLocation,
	}
	logrus.Infof("in StartBackupRetentionCheckDaemon")
	c := cron.New()
	if retentionSchedule == "" {
		retentionSchedule = EnforceRetentionInterval
	}
	schedule, err := cron.ParseStandard(retentionSchedule)
	if err != nil {
		logrus.Errorf("StartBackupRetentionCheckDaemon: Error parsing cron schedule: %v", err)
		return
	}

	kubeSystemNS, err := namespaces.Get("kube-system", k8sv1.GetOptions{})
	if err != nil {
		logrus.Fatalf("Error getting namespace kube-system %v", err)
	}
	recurringSync.kubeSystemNS = string(kubeSystemNS.UID)

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
	fileCreationTimeCutoff, err := getCutoffTime(backup)
	if err != nil {
		return err
	}
	logrus.Infof("Retaining files created at or after : %v", fileCreationTimeCutoff)

	var backupLocation string
	if backup.Spec.StorageLocation == nil {
		backupLocation = b.defaultBackupLocation
	} else if backup.Spec.StorageLocation.Local != "" {
		backupLocation = backup.Spec.StorageLocation.Local
	} else if backup.Spec.StorageLocation.S3 != nil {
		s3Client, err := objectstore.GetS3Client(b.ctx, backup.Spec.StorageLocation.S3, backup.Namespace, b.dynamicClient)
		if err != nil {
			return err
		}
		return b.deleteS3Backups(backup, s3Client)
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
		if fileCreationTime.Before(fileCreationTimeCutoff) {
			logrus.Infof("File %v was created at %v, deleting it to follow retention policy", file, fileCreationTime)
			if err := os.Remove(file); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b backupRetentionSync) deleteS3Backups(backup *v1.Backup, svc *minio.Client) error {
	var backupDeleteList []string
	cutoffTime, err := getCutoffTime(backup)
	if err != nil {
		return err
	}

	// Create a done channel to control 'ListObjectsV2' go routine.
	doneCh := make(chan struct{})

	// Indicate to our routine to exit cleanly upon return.
	defer close(doneCh)

	isRecursive := false
	prefix := ""
	s3 := backup.Spec.StorageLocation.S3
	if len(s3.Folder) != 0 {
		prefix = s3.Folder
		// Recurse will show us the files in the folder
		isRecursive = true
	}
	objectCh := svc.ListObjects(s3.BucketName, prefix, isRecursive, doneCh)
	// default-backup-([a-z0-9-]).*([tar]).gz
	// default-test-ecm-backup-24e1b8ce-1f00-4bbe-94bb-248ad7606dc8-([0-9-#]).*tar.gz$
	re := regexp.MustCompile(fmt.Sprintf("%s-%s-%s-([0-9-#]).*tar.gz$", backup.Namespace, backup.Name, b.kubeSystemNS))
	for object := range objectCh {
		if object.Err != nil {
			logrus.Error("error to fetch s3 file:", object.Err)
			return object.Err
		}
		// only parse backup file names that matches *_etcd format
		if re.MatchString(object.Key) {
			filename := object.Key

			if len(s3.Folder) != 0 {
				// example object.Key with folder: folder/timestamp_etcd.zip
				// folder and separator needs to be stripped so time can be parsed below
				logrus.Infof("Stripping [%s] from [%s]", fmt.Sprintf("%s/", prefix), filename)
				filename = strings.TrimPrefix(filename, fmt.Sprintf("%s/", prefix))
			}
			logrus.Infof("object.Key: [%s], filename: [%s]", object.Key, filename)
			filename = strings.Replace(filename, "#", ":", -1)
			logrus.Infof("Filename with TS: %s", filename)
			timestampTGZ := strings.TrimPrefix(filename, fmt.Sprintf("%s-%s-%s-", backup.Namespace, backup.Name, b.kubeSystemNS))
			logrus.Infof("timestamp with TS: %s", timestampTGZ)
			timestamp := strings.TrimSuffix(timestampTGZ, ".tar.gz")
			logrus.Infof("Parsing time: %s", timestamp)
			backupTime, err := time.Parse(time.RFC3339, timestamp)
			if err != nil {
				fmt.Printf("\nNOOO errror: %v\n", err)
				return err
			}
			if backupTime.Before(cutoffTime) {
				// We use object.Key here as we need the full path when a folder is used
				logrus.Infof("Adding [%s] to files to delete, backupTime: [%q], cutoffTime: [%q]", object.Key, backupTime, cutoffTime)
				backupDeleteList = append(backupDeleteList, object.Key)
			}
		}
	}
	logrus.Infof("Found %d files to delete", len(backupDeleteList))

	for i := range backupDeleteList {
		logrus.Infof("Start to delete s3 backup file [%s]", backupDeleteList[i])
		err := svc.RemoveObject(s3.BucketName, backupDeleteList[i])
		if err != nil {
			logrus.Errorf("Error detected during deletion: %v", err)
			return err
		}
		logrus.Infof("Success delete s3 backup file [%s]", backupDeleteList[i])
	}
	return nil
}

func getCutoffTime(backup *v1.Backup) (time.Time, error) {
	var cutoff time.Time
	retention := backup.Spec.Retention
	if retention == "" {
		retention = DefaultRetentionTime
	}
	// example, retentionTime = 6 hours
	retentionTime, err := time.ParseDuration(retention)
	if err != nil {
		return cutoff, err
	}
	logrus.Infof("Retention time for recurring backups for backup %v: %v", backup.Name, retentionTime.Hours())
	// files created 6 hours before now must be deleted
	cutoff = time.Now().Add(-retentionTime)
	return cutoff, nil
}
