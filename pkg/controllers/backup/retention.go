package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	resourceController "github.com/rancher/backup-restore-operator/pkg/generated/controllers/resources.cattle.io/v1"
	"github.com/robfig/cron"
	"github.com/sirupsen/logrus"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const EnforceRetentionInterval = "@every 1m"

var recurringSync *backupRetentionSync

type backupRetentionSync struct {
	ctx                   context.Context
	backups               resourceController.BackupController
	defaultBackupLocation string
}

func StartBackupRetentionCheckDaemon(ctx context.Context, backups resourceController.BackupController,
	syncRecurringBackupsSchedule, DefaultBackupLocation string) {
	recurringSync = &backupRetentionSync{
		ctx:                   ctx,
		backups:               backups,
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
