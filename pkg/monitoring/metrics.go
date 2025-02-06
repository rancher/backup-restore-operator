package monitoring

import (
	"net/http"
	"strconv"
	"time"

	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	controllers "github.com/rancher/backup-restore-operator/pkg/generated/controllers/resources.cattle.io/v1"
	"github.com/sirupsen/logrus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	backup = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "rancher_backup",
			Help: "Details on a specific Rancher Backup CR",
			// add labels showing encryption type and current status
		}, []string{"name", "resourceSetName", "retentionCount", "backupType", "filename", "storageLocation", "nextSnapshot", "lastSnapshot"},
	)

	backupCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "rancher_backup_count",
			Help: "Number of existing Rancher Backup CRs",
		},
	)

	backupsAttempted = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rancher_backups_attempted",
			Help: "Number of Rancher Backups processed by this operator",
		}, []string{"name"},
	)

	backupsFailed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rancher_backups_failed",
			Help: "Number of failed Rancher Backups processed by this operator",
		}, []string{"name"},
	)

	backupDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "rancher_backup_duration_ms",
			Help: "Duration of each backup processed by this operator",
		}, []string{"name"},
	)

	backupLastProcessed = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "rancher_backup_last_processed",
			Help: "Unix time of when the last Backup was processed (in seconds)",
		}, []string{"name"},
	)

	restore = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "rancher_restore",
			Help: "Details on a specific Rancher Restore CR",
		}, []string{"name", "fileName", "prune", "storageLocation", "restoreTime"},
	)

	restoreCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "rancher_restore_count",
			Help: "Number of existing Rancher Restore CRs",
		},
	)
)

func updateBackupMetrics(backups []v1.Backup) {
	count := len(backups)
	backupCount.Set(float64(count))

	backup.Reset()

	var backupType, backupNextSnapshot string
	for _, b := range backups {
		backupType = b.Status.BackupType
		if backupType == "One-time" {
			backupNextSnapshot = "N/A - One-time Backup"
		} else {
			backupNextSnapshot = b.Status.NextSnapshotAt
		}

		backup.WithLabelValues(
			b.Name,
			b.Spec.ResourceSetName,
			strconv.Itoa(int(b.Spec.RetentionCount)),
			backupType,
			b.Status.Filename,
			b.Status.StorageLocation,
			backupNextSnapshot,
			b.Status.LastSnapshotTS,
		).Set(1)
	}
}

func updateRestoreMetrics(restores []v1.Restore) {
	count := len(restores)
	restoreCount.Set(float64(count))

	restore.Reset()

	for _, r := range restores {
		restore.WithLabelValues(
			r.Name,
			r.Spec.BackupFilename,
			strconv.FormatBool(*r.Spec.Prune),
			r.Status.BackupSource,
			r.Status.RestoreCompletionTS,
		).Set(1)
	}
}

func StartmMetadataMetricsCollection(backups controllers.BackupController, restores controllers.RestoreController) {
	var backupList *v1.BackupList
	var restoreList *v1.RestoreList

	var err error

	ticker := time.NewTicker(90 * time.Second)
	for range ticker.C {
		logrus.Debugf("Collecting metadata to populate metrics")

		getBackupsErr := retry.OnError(retry.DefaultRetry,
			func(err error) bool {
				logrus.Warnf("Retrying listing Backup CRs: %s", err)
				return true
			}, func() error {
				backupList, err = backups.List(k8sv1.ListOptions{})
				return err
			})

		if getBackupsErr != nil {
			logrus.Errorf("Failed collecting backup metadata to populate metrics: %s", getBackupsErr)
		}

		getRestoresErr := retry.OnError(retry.DefaultRetry,
			func(err error) bool {
				logrus.Warnf("Retrying listing Restore CRs: %s", err)
				return true
			}, func() error {
				restoreList, err = restores.List(k8sv1.ListOptions{})
				return err
			})

		if getRestoresErr != nil {
			logrus.Errorf("Failed collecting restore metadata to populate metrics: %s", getRestoresErr)
		}

		updateBackupMetrics(backupList.Items)
		updateRestoreMetrics(restoreList.Items)
	}
}

func InitMetricsServer() {
	metrics.Registry.MustRegister(
		backup,
		backupCount,
		backupsAttempted,
		backupsFailed,
		backupDuration,
		backupLastProcessed,
	)

	http.Handle("/metrics", promhttp.Handler())
	http.ListenAndServe(":8080", nil)
}
