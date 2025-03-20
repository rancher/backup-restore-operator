package monitoring

import (
	"fmt"
	"strings"
	"testing"
	"time"

	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func resetMetrics() {
	backupDuration.Reset()
	backupLastProcessed.Reset()
}

func TestUpdateTimeSensitiveBackupMetrics(t *testing.T) {
	t.Cleanup(resetMetrics)

	mockBackupName := "backup1"
	mockEndTime := time.Now().Unix()
	mockTotalTime := int64(1500) // 1.5 seconds

	UpdateTimeSensitiveBackupMetrics(mockBackupName, mockEndTime, mockTotalTime)

	const expectedDuration = `
# HELP rancher_backup_duration_ms Duration of each backup processed by this operator in ms
# TYPE rancher_backup_duration_ms histogram
rancher_backup_duration_ms_bucket{name="backup1",le="500"} 0
rancher_backup_duration_ms_bucket{name="backup1",le="1000"} 0
rancher_backup_duration_ms_bucket{name="backup1",le="2500"} 1
rancher_backup_duration_ms_bucket{name="backup1",le="5000"} 1
rancher_backup_duration_ms_bucket{name="backup1",le="7500"} 1
rancher_backup_duration_ms_bucket{name="backup1",le="10000"} 1
rancher_backup_duration_ms_bucket{name="backup1",le="30000"} 1
rancher_backup_duration_ms_bucket{name="backup1",le="60000"} 1
rancher_backup_duration_ms_bucket{name="backup1",le="120000"} 1
rancher_backup_duration_ms_bucket{name="backup1",le="+Inf"} 1
rancher_backup_duration_ms_sum{name="backup1"} 1500
rancher_backup_duration_ms_count{name="backup1"} 1
`

	err := promtestutil.CollectAndCompare(backupDuration, strings.NewReader(expectedDuration), "rancher_backup_duration_ms")
	if err != nil {
		t.Error("error when comparing resulting rancher_backup_duration_ms to expected values:", err)
	}

	const expectedLastTemplate = `
# HELP rancher_backup_last_processed Unix time of when the last Backup was processed (in seconds)
# TYPE rancher_backup_last_processed gauge
rancher_backup_last_processed{name="backup1"} %v
`
	expectedLast := fmt.Sprintf(expectedLastTemplate, float64(mockEndTime))

	err = promtestutil.CollectAndCompare(backupLastProcessed, strings.NewReader(expectedLast), "rancher_backup_last_processed")
	if err != nil {
		t.Error("error when comparing resulting rancher_backup_last_processed to expected values:", err)
	}
}

func TestUpdateTimeSensitiveBackupMetricsRecurring(t *testing.T) {
	t.Cleanup(resetMetrics)

	backupName := "backup2"
	endTime := time.Now().Unix()
	totalTime := int64(1500) // 1.5 seconds

	UpdateTimeSensitiveBackupMetrics(backupName, endTime, totalTime)

	// Simulate a recurring backup by updating the metrics again
	endTime = time.Now().Unix()
	totalTime = int64(2700) // 2.7 seconds

	UpdateTimeSensitiveBackupMetrics(backupName, endTime, totalTime)

	const expectedDuration = `
# HELP rancher_backup_duration_ms Duration of each backup processed by this operator in ms
# TYPE rancher_backup_duration_ms histogram
rancher_backup_duration_ms_bucket{name="backup2",le="500"} 0
rancher_backup_duration_ms_bucket{name="backup2",le="1000"} 0
rancher_backup_duration_ms_bucket{name="backup2",le="2500"} 1
rancher_backup_duration_ms_bucket{name="backup2",le="5000"} 2
rancher_backup_duration_ms_bucket{name="backup2",le="7500"} 2
rancher_backup_duration_ms_bucket{name="backup2",le="10000"} 2
rancher_backup_duration_ms_bucket{name="backup2",le="30000"} 2
rancher_backup_duration_ms_bucket{name="backup2",le="60000"} 2
rancher_backup_duration_ms_bucket{name="backup2",le="120000"} 2
rancher_backup_duration_ms_bucket{name="backup2",le="+Inf"} 2
rancher_backup_duration_ms_sum{name="backup2"} 4200
rancher_backup_duration_ms_count{name="backup2"} 2
`

	err := promtestutil.CollectAndCompare(backupDuration, strings.NewReader(expectedDuration))
	if err != nil {
		t.Error("error when comparing resulting rancher_backup_duration_ms to expected values:", err)
	}

	const expectedLastTemplate = `
# HELP rancher_backup_last_processed Unix time of when the last Backup was processed (in seconds)
# TYPE rancher_backup_last_processed gauge
rancher_backup_last_processed{name="backup2"} %v
`
	expectedLast := fmt.Sprintf(expectedLastTemplate, float64(endTime))

	err = promtestutil.CollectAndCompare(backupLastProcessed, strings.NewReader(expectedLast))
	if err != nil {
		t.Error("error when comparing resulting rancher_backup_last_processed to expected values:", err)
	}
}

func TestUpdateProcessedBackupMetrics(t *testing.T) {
	t.Cleanup(resetMetrics)

	backupName := "backup1"
	var err error

	// Test case: Successful backup
	UpdateProcessedBackupMetrics(backupName, &err)
	UpdateProcessedBackupMetrics(backupName, &err)

	const expectedAttempted = `
# HELP rancher_backups_attempted Number of Rancher Backups processed by this operator
# TYPE rancher_backups_attempted counter
rancher_backups_attempted{name="backup1"} 2
`
	if err := promtestutil.CollectAndCompare(backupsAttempted, strings.NewReader(expectedAttempted), "rancher_backups_attempted"); err != nil {
		t.Error("error when comparing resulting rancher_backups_attempted to expected values:", err)
	}

	const expectedFailed = `
# HELP rancher_backups_failed Number of failed Rancher Backups processed by this operator
# TYPE rancher_backups_failed counter
rancher_backups_failed{name="backup1"} 0
`
	if err := promtestutil.CollectAndCompare(backupsFailed, strings.NewReader(expectedFailed), "rancher_backups_failed"); err != nil {
		t.Error("error when comparing resulting rancher_backups_failed to expected values:", err)
	}

	// Test case: Failed backup
	err = fmt.Errorf("backup failed2")
	UpdateProcessedBackupMetrics(backupName, &err)

	const expectedFailedAfterError = `
# HELP rancher_backups_failed Number of failed Rancher Backups processed by this operator
# TYPE rancher_backups_failed counter
rancher_backups_failed{name="backup1"} 1
`
	if err := promtestutil.CollectAndCompare(backupsFailed, strings.NewReader(expectedFailedAfterError), "rancher_backups_failed"); err != nil {
		t.Error("error when comparing resulting rancher_backups_failed to expected values:", err)
	}
}

func TestUpdateRestoreMetrics(t *testing.T) {
	t.Cleanup(resetMetrics)
	tr := true
	f := false

	restores := []v1.Restore{
		{
			ObjectMeta: k8sv1.ObjectMeta{Name: "restore1"},
			Status: v1.RestoreStatus{
				Conditions: []genericcondition.GenericCondition{
					{Message: "Restore completed successfully"},
				},
				BackupSource:        "s3",
				RestoreCompletionTS: "1627849200",
			},
			Spec: v1.RestoreSpec{
				BackupFilename: "backup1.tar.gz",
				Prune:          &tr,
			},
		},
		{
			ObjectMeta: k8sv1.ObjectMeta{Name: "restore2"},
			Status: v1.RestoreStatus{
				Conditions: []genericcondition.GenericCondition{
					{Message: "Restore failed"},
				},
				BackupSource:        "s3",
				RestoreCompletionTS: "1627849300",
			},
			Spec: v1.RestoreSpec{
				BackupFilename: "backup2.tar.gz",
				Prune:          &f,
			},
		},
	}

	updateRestoreMetrics(restores)

	const expectedRestoreCount = `
# HELP rancher_restore_count Number of existing Rancher Restore CRs
# TYPE rancher_restore_count gauge
rancher_restore_count 2
`
	if err := promtestutil.CollectAndCompare(restoreCount, strings.NewReader(expectedRestoreCount), "rancher_restore_count"); err != nil {
		t.Error("error when comparing resulting rancher_restore_count to expected values:", err)
	}

	const expectedRestore = `
# HELP rancher_restore Details on a specific Rancher Restore CR
# TYPE rancher_restore gauge
rancher_restore{fileName="backup1.tar.gz",name="restore1",prune="true",restoreTime="1627849200",status="Restore completed successfully",storageLocation="s3"} 1
rancher_restore{fileName="backup2.tar.gz",name="restore2",prune="false",restoreTime="1627849300",status="Restore failed",storageLocation="s3"} 1
`
	if err := promtestutil.CollectAndCompare(restore, strings.NewReader(expectedRestore), "rancher_restore"); err != nil {
		t.Error("error when comparing resulting rancher_restore to expected values:", err)
	}
}

func TestUpdateBackupMetrics(t *testing.T) {
	t.Cleanup(resetMetrics)

	backups := []v1.Backup{
		{
			ObjectMeta: k8sv1.ObjectMeta{Name: "backup1"},
			Status: v1.BackupStatus{
				BackupType:      "One-time",
				NextSnapshotAt:  "N/A - One-time Backup",
				Filename:        "backup1.tar.gz",
				StorageLocation: "s3",
				LastSnapshotTS:  "1627849200",
				Conditions: []genericcondition.GenericCondition{
					{Message: "Backup completed successfully"},
				},
			},
			Spec: v1.BackupSpec{
				ResourceSetName: "resourceSet1",
				RetentionCount:  3,
			},
		},
		{
			ObjectMeta: k8sv1.ObjectMeta{Name: "backup2"},
			Status: v1.BackupStatus{
				BackupType:      "Scheduled",
				NextSnapshotAt:  "1627849300",
				Filename:        "backup2.tar.gz",
				StorageLocation: "s3",
				LastSnapshotTS:  "1627849300",
				Conditions: []genericcondition.GenericCondition{
					{Message: "Backup failed"},
				},
			},
			Spec: v1.BackupSpec{
				ResourceSetName: "resourceSet2",
				RetentionCount:  5,
			},
		},
	}

	updateBackupMetrics(backups)

	const expectedBackupCount = `
# HELP rancher_backup_count Number of existing Rancher Backup CRs
# TYPE rancher_backup_count gauge
rancher_backup_count 2
`
	if err := promtestutil.CollectAndCompare(backupCount, strings.NewReader(expectedBackupCount), "rancher_backup_count"); err != nil {
		t.Error("error when comparing resulting rancher_backup_count to expected values:", err)
	}

	const expectedBackup = `
# HELP rancher_backup Details on a specific Rancher Backup CR
# TYPE rancher_backup gauge
rancher_backup{backupType="One-time",filename="backup1.tar.gz",lastSnapshot="1627849200",name="backup1",nextSnapshot="N/A - One-time Backup",resourceSetName="resourceSet1",retentionCount="3",status="Backup completed successfully",storageLocation="s3"} 1
rancher_backup{backupType="Scheduled",filename="backup2.tar.gz",lastSnapshot="1627849300",name="backup2",nextSnapshot="1627849300",resourceSetName="resourceSet2",retentionCount="5",status="Backup failed",storageLocation="s3"} 1
`
	if err := promtestutil.CollectAndCompare(backup, strings.NewReader(expectedBackup), "rancher_backup"); err != nil {
		t.Error("error when comparing resulting rancher_backup to expected values:", err)
	}
}
