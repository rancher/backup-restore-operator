package backup

import (
	"context"
	"time"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	backupController "github.com/rancher/backup-restore-operator/pkg/generated/controllers/resources.cattle.io/v1"
	"github.com/rancher/wrangler/pkg/condition"
	"github.com/robfig/cron"
	"github.com/sirupsen/logrus"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const DefaultSyncTimeForRecurringBackups = "@every 30s"

type backupSync struct {
	ctx     context.Context
	backups backupController.BackupController
}

var recurringSync *backupSync

func StartRecurringBackupsDaemon(ctx context.Context, backups backupController.BackupController, syncRecurringBackupsSchedule string) {
	recurringSync = &backupSync{
		ctx:     ctx,
		backups: backups,
	}
	logrus.Infof("in StartRecurringBackupsDaemon")
	c := cron.New()
	if syncRecurringBackupsSchedule == "" {
		syncRecurringBackupsSchedule = DefaultSyncTimeForRecurringBackups
	}
	schedule, err := cron.ParseStandard(syncRecurringBackupsSchedule)
	if err != nil {
		logrus.Errorf("StartRecurringBackupsDaemon: Error parsing cron schedule: %v", err)
		return
	}
	logrus.Infof("in StartRecurringBackupsDaemon adding job, schedule: %v", schedule.Next(time.Now()))
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

func (b backupSync) syncBackups() {
	logrus.Infof("Checking for performing recurring backup")
	backups, err := b.backups.List("", k8sv1.ListOptions{})
	if err != nil {
		logrus.Errorf("syncBackups: Error listing backups: %v", err)
	}
	for _, backup := range backups.Items {
		if backup.Spec.Schedule == "" {
			continue
		}
		currBackupSchedule, err := cron.ParseStandard(backup.Spec.Schedule)
		if err != nil {
			logrus.Errorf("syncBackups: Error parsing cron schedule: %v", err)
			continue
		}
		if backup.Status.LastSnapshotTS == "" {
			// no backups were performed for this spec, enqueue it
			logrus.Infof("Performing backup for %v", backup.Name)
			condition.Cond(v1.BackupConditionTriggered).SetStatusBool(&backup, true)
			_, err := b.backups.UpdateStatus(&backup)
			if err != nil {
				logrus.Errorf("Error triggering backup: %v", err)
				continue
			}
			//b.backups.Enqueue(backup.Namespace, backup.Name)
			continue
		}
		lastBackupTime, err := time.Parse(time.RFC3339, backup.Status.LastSnapshotTS)
		if err != nil {
			logrus.Errorf("Error parsing last backup time: %v", err)
			continue
		}
		nextBackupTime := currBackupSchedule.Next(lastBackupTime)
		if nextBackupTime.After(time.Now()) {
			// there's time left before taking another backup
			continue
		}
		// else in either case, whether current time matches or is after curr time, take backup
		logrus.Infof("Performing backup for %v", backup.Name)
		condition.Cond(v1.BackupConditionTriggered).SetStatusBool(&backup, true)
		_, err = b.backups.UpdateStatus(&backup)
		if err != nil {
			logrus.Errorf("Error triggering backup: %v", err)
			continue
		}
		//b.backups.Enqueue(backup.Namespace, backup.Name)
	}
}
