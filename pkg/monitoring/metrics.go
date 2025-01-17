package monitoring

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	backupCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "bro_backup_count",
			Help: "Number of existing Rancher Backup CRs",
		})

	backup = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "bro_backup",
			Help: "Details on a specific Rancher Backup CR",
			// add labels showing encryption type and current status
		}, []string{"name", "resourceSetName", "retentionCount", "backupType", "filename", "storageLocation", "nextSnapshot", "lastSnapshot"})

	backupLastProcessed = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "bro_backup_last_processed",
			Help: "Unix time of when the last Backup was processed (in seconds)",
		})

	backupFailed = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "bro_backup_failed",
			Help: "Indicates whether the last Backup to be processed failed or not",
		})

	restoreCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "bro_restore_count",
			Help: "Number of existing Rancher Restore CRs",
		})
)

func UpdateBackupMetrics(backups []v1.Backup) {
	count := len(backups)
	backupCount.Set(float64(count))

	backup.Reset()

	for _, b := range backups {
		backup.WithLabelValues(
			b.Name,
			b.Spec.ResourceSetName,
			strconv.Itoa(int(b.Spec.RetentionCount)),
			b.Status.BackupType,
			b.Status.Filename,
			b.Status.StorageLocation,
			b.Status.NextSnapshotAt,
			b.Status.LastSnapshotTS,
		).Set(1)
	}
}

func UpdateBackupLastProcessedMetrics(err *error) {
	backupLastProcessed.SetToCurrentTime()

	if *err != nil {
		backupFailed.Set(1)
		return
	}

	backupFailed.Set(0)
}

func UpdateRestoreMetrics(restores *v1.RestoreList) {

}

func StartMetricsServer() {
	metrics.Registry.MustRegister(backupCount, restoreCount, backup)

	http.Handle("/metrics", promhttp.Handler())
	http.ListenAndServe(":8080", nil)
}
