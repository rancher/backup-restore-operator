package main

import (
	"flag"
	"os"

	"github.com/rancher/wrangler/v3/pkg/kubeconfig"
	"github.com/rancher/wrangler/v3/pkg/signals"
	"github.com/sirupsen/logrus"

	"github.com/rancher/backup-restore-operator/cmd/operator/version"
	"github.com/rancher/backup-restore-operator/pkg/operator"
	backuputil "github.com/rancher/backup-restore-operator/pkg/util"
)

const (
	LogFormat = "2006/01/02 15:04:05"
)

var (
	LocalBackupStorageLocation      = "/var/lib/backups" // local within the pod, this is the mountPath for PVC
	KubeConfig                      string
	OperatorPVEnabled               string
	MetricsServerEnabled            string
	OperatorS3BackupStorageLocation string
	ChartNamespace                  string
	Debug                           bool
	Trace                           bool
)

func init() {
	flag.StringVar(&KubeConfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.BoolVar(&Debug, "debug", false, "Enable debug logging.")
	flag.BoolVar(&Trace, "trace", false, "Enable trace logging.")

	flag.Parse()
	OperatorPVEnabled = os.Getenv("DEFAULT_PERSISTENCE_ENABLED")
	OperatorS3BackupStorageLocation = os.Getenv("DEFAULT_S3_BACKUP_STORAGE_LOCATION")
	ChartNamespace = os.Getenv("CHART_NAMESPACE")
	MetricsServerEnabled = os.Getenv("METRICS_SERVER")
}

func main() {
	logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true, ForceColors: true, TimestampFormat: LogFormat})
	if Debug {
		logrus.SetLevel(logrus.DebugLevel)
		logrus.Debugf("Loglevel set to [%v]", logrus.DebugLevel)
	}
	if Trace {
		logrus.SetLevel(logrus.TraceLevel)
		logrus.Tracef("Loglevel set to [%v]", logrus.TraceLevel)
	}

	logrus.Infof("Starting backup-restore-operator version %s (%s) [built at %s]", version.Version, version.GitCommit, version.Date)
	ctx := signals.SetupSignalContext()
	restKubeConfig, err := kubeconfig.GetNonInteractiveClientConfig(KubeConfig).ClientConfig()
	if err != nil {
		logrus.Fatalf("failed to find kubeconfig: %v", err)
	}

	dm := os.Getenv("CATTLE_DEV_MODE")
	backuputil.SetDevMode(dm != "")
	runOptions := operator.RunOptions{
		OperatorPVCEnabled:              OperatorPVEnabled != "",
		MetricsServerEnabled:            MetricsServerEnabled != "",
		MetricsPort:                     8080,
		MetricsIntervalSeconds:          60,
		OperatorS3BackupStorageLocation: OperatorS3BackupStorageLocation,
		ChartNamespace:                  ChartNamespace,
		LocalDriverPath:                 "",
	}

	if err := operator.Run(ctx, restKubeConfig, runOptions); err != nil {
		logrus.Fatalf("Error running operator: %s", err.Error())
	}
}
