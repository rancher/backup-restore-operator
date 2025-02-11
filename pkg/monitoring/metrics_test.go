package monitoring

import (
	"fmt"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"strings"
	"testing"
	"time"
)

func resetMetrics() {
	backupDuration.Reset()
	backupLastProcessed.Reset()
}

func TestUpdateTimeSensitiveBackupMetrics(t *testing.T) {
	t.Cleanup(resetMetrics)
	// Setup a test backup name and time values
	backupName := "backup1"
	endTime := time.Now().Unix() // current Unix timestamp
	totalTime := int64(1500)     // 1.5 seconds

	// Call the function that updates the metrics
	UpdateTimeSensitiveBackupMetrics(backupName, endTime, totalTime)

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

	err := promtestutil.CollectAndCompare(backupDuration, strings.NewReader(expectedDuration))
	if err != nil {
		t.Error(err)
	}

	const expectedLastTemplate = `
# HELP rancher_backup_last_processed Unix time of when the last Backup was processed (in seconds)
# TYPE rancher_backup_last_processed gauge
rancher_backup_last_processed{name="backup1"} %v
`
	expectedLast := fmt.Sprintf(expectedLastTemplate, float64(endTime))

	err = promtestutil.CollectAndCompare(backupLastProcessed, strings.NewReader(expectedLast))
	if err != nil {
		t.Error(err)
	}
}

func TestUpdateTimeSensitiveBackupMetricsMore(t *testing.T) {
	t.Cleanup(resetMetrics)
	// Setup a test backup name and time values
	backupName := "backup2"
	endTime := time.Now().Unix() // current Unix timestamp
	totalTime := int64(25000)

	// Call the function that updates the metrics
	UpdateTimeSensitiveBackupMetrics(backupName, endTime, totalTime)

	const expectedDuration = `
# HELP rancher_backup_duration_ms Duration of each backup processed by this operator in ms
# TYPE rancher_backup_duration_ms histogram
rancher_backup_duration_ms_bucket{name="backup2",le="500"} 0
rancher_backup_duration_ms_bucket{name="backup2",le="1000"} 0
rancher_backup_duration_ms_bucket{name="backup2",le="2500"} 0
rancher_backup_duration_ms_bucket{name="backup2",le="5000"} 0
rancher_backup_duration_ms_bucket{name="backup2",le="7500"} 0
rancher_backup_duration_ms_bucket{name="backup2",le="10000"} 0
rancher_backup_duration_ms_bucket{name="backup2",le="+Inf"} 1
rancher_backup_duration_ms_sum{name="backup2"} 25000
rancher_backup_duration_ms_count{name="backup2"} 1
`

	err := promtestutil.CollectAndCompare(backupDuration, strings.NewReader(expectedDuration))
	if err != nil {
		t.Error(err)
	}

	const expectedLastTemplate = `
# HELP rancher_backup_last_processed Unix time of when the last Backup was processed (in seconds)
# TYPE rancher_backup_last_processed gauge
rancher_backup_last_processed{name="backup2"} %v
`
	expectedLast := fmt.Sprintf(expectedLastTemplate, float64(endTime))

	err = promtestutil.CollectAndCompare(backupLastProcessed, strings.NewReader(expectedLast))
	if err != nil {
		t.Error(err)
	}
}
