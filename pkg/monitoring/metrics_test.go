package monitoring

import (
	"fmt"
	"strings"
	"testing"
	"time"

	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
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
