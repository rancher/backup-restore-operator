package monitoring

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
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
	defaultRancherBackupDurationBuckets = []float64{0.5, 1, 2.5, 5, 7.5, 10, 30, 60, 120}

	backup = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "rancher_backup_info",
			Help: "Details on a specific Rancher Backup CR",
		}, []string{"name", "status", "resourceSetName", "retentionCount", "backupType", "filename", "storageLocation", "nextSnapshot", "lastSnapshot"},
	)

	backupCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "rancher_backup_count",
			Help: "Number of existing Rancher Backup CRs",
		},
	)

	backupsAttempted = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rancher_backups_attempted_total",
			Help: "Number of Rancher Backups processed by this operator",
		}, []string{"name"},
	)

	backupsFailed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rancher_backups_failed_total",
			Help: "Number of failed Rancher Backups processed by this operator",
		}, []string{"name"},
	)

	backupDuration *prometheus.HistogramVec

	backupLastProcessed = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "rancher_backup_last_processed_timestamp_seconds",
			Help: "Unix time of when the last Backup was processed (in seconds)",
		}, []string{"name"},
	)

	restore = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "rancher_restore_info",
			Help: "Details on a specific Rancher Restore CR",
		}, []string{"name", "status", "fileName", "prune", "storageLocation", "restoreTime"},
	)

	restoreCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "rancher_restore_count",
			Help: "Number of existing Rancher Restore CRs",
		},
	)
)

func init() {
	buckets := defaultRancherBackupDurationBuckets
	rancherBackupDurationBuckets := os.Getenv("BACKUP_DURATION_BUCKETS")

	if rancherBackupDurationBuckets != "" {
		buckets = []float64{}
		for _, b := range strings.Split(rancherBackupDurationBuckets, ",") {
			f, err := strconv.ParseFloat(strings.TrimSpace(b), 64)
			if err != nil {
				logrus.Errorf("Failed to parse backup duration bucket '%s': %v", b, err)
				return
			}
			buckets = append(buckets, f)
		}
	}

	logrus.Debugf("Backup duration buckets: %v", buckets)
	backupDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "rancher_backup_duration_seconds",
			Help:    "Duration of each backup processed by this operator in seconds",
			Buckets: buckets,
		}, []string{"name"},
	)
}

func updateBackupMetrics(backups []v1.Backup) {
	count := len(backups)
	backupCount.Set(float64(count))

	backup.Reset()
	var backupType v1.BackupType
	var backupNextSnapshot, backupMessage string
	for _, b := range backups {
		backupType = b.Status.BackupType
		if backupType == v1.OneTimeBackupType {
			backupNextSnapshot = "N/A - One-time Backup"
		} else {
			backupNextSnapshot = b.Status.NextSnapshotAt
		}

		if len(b.Status.Conditions) > 0 {
			backupMessage = b.Status.Conditions[0].Message
		}

		backup.WithLabelValues(
			b.Name,
			backupMessage,
			b.Spec.ResourceSetName,
			strconv.Itoa(int(b.Spec.RetentionCount)),
			string(backupType),
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

	var restoreMessage string
	for _, r := range restores {

		if len(r.Status.Conditions) > 0 {
			restoreMessage = r.Status.Conditions[0].Message
		}

		restore.WithLabelValues(
			r.Name,
			restoreMessage,
			r.Spec.BackupFilename,
			strconv.FormatBool(*r.Spec.Prune),
			r.Status.BackupSource,
			r.Status.RestoreCompletionTS,
		).Set(1)
	}
}

func UpdateProcessedBackupMetrics(backup string, err *error) {
	backupsAttempted.WithLabelValues(backup).Inc()

	if *err != nil {
		backupsFailed.WithLabelValues(backup).Inc()
		return
	}

	backupsFailed.WithLabelValues(backup)
}

func UpdateTimeSensitiveBackupMetrics(backup string, endTime float64, totalTime float64) {
	backupDuration.WithLabelValues(backup).Observe(totalTime)
	backupLastProcessed.WithLabelValues(backup).Set(endTime)
}

func StartRestoreMetricsCollection(
	restores controllers.RestoreController,
	interval int,
) {
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	var err error
	var restoreList *v1.RestoreList
	for range ticker.C {
		logrus.Debug("Collecting restore metadata to populate metrics")

		getRestoresErr := retry.OnError(retry.DefaultRetry,
			func(err error) bool {
				logrus.Warnf("Retrying listing Backup CRs: %s", err)
				return true
			}, func() error {
				restoreList, err = restores.List(k8sv1.ListOptions{})
				return err
			})
		if getRestoresErr != nil {
			logrus.Errorf("Failed collecting restore metadata to populate metrics: %s", getRestoresErr)
		}

		updateRestoreMetrics(restoreList.Items)
	}

	logrus.Info("shutting down restore metrics metadata collection...")
}

func StartBackupMetricsCollection(
	backups controllers.BackupController,
	interval int,
) {
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	var err error
	var backupList *v1.BackupList
	for range ticker.C {
		logrus.Debug("Collecting backup metadata to populate metrics")

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

		updateBackupMetrics(backupList.Items)
	}

	logrus.Info("shutting down backup metrics metadata collection...")
}

func InitMetricsServer(port int) {
	metrics.Registry.MustRegister(
		backup,
		backupCount,
		backupsAttempted,
		backupsFailed,
		backupDuration,
		backupLastProcessed,
	)

	http.Handle("/metrics", promhttp.Handler())
	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), nil); err != nil {
		logrus.Fatalf("failed to start metrics server : %s", err)
	}

	logrus.Info("Shutting down prometheus metrics server")
}
