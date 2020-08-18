//go:generate go run pkg/codegen/cleanup/main.go
//go:generate /bin/rm -rf pkg/generated
//go:generate go run pkg/codegen/main.go

package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"

	"github.com/ehazlett/simplelog"
	"github.com/rancher/backup-restore-operator/pkg/controllers/backup"
	"github.com/rancher/backup-restore-operator/pkg/controllers/restore"
	"github.com/rancher/backup-restore-operator/pkg/generated/controllers/resources.cattle.io"
	"github.com/rancher/backup-restore-operator/pkg/util"
	lasso "github.com/rancher/lasso/pkg/client"
	"github.com/rancher/lasso/pkg/mapper"
	v1core "github.com/rancher/wrangler-api/pkg/generated/controllers/core"
	"github.com/rancher/wrangler/pkg/kubeconfig"
	"github.com/rancher/wrangler/pkg/ratelimit"
	"github.com/rancher/wrangler/pkg/signals"
	"github.com/rancher/wrangler/pkg/start"
	"github.com/sirupsen/logrus"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/dynamic"
)

var (
	Version                      = "v0.0.0-dev"
	GitCommit                    = "HEAD"
	KubeConfig                   string
	DefaultBackupStorageLocation string
	ChartNamespace               string
)

func init() {
	flag.StringVar(&KubeConfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.Parse()
	DefaultBackupStorageLocation = os.Getenv("DEFAULT_BACKUP_STORAGE_LOCATION")
	ChartNamespace = os.Getenv("CHART_NAMESPACE")
}

func main() {
	logrus.Info("Starting controller")
	logrus.SetFormatter(&simplelog.StandardFormatter{})
	ctx := signals.SetupSignalHandler(context.Background())
	restKubeConfig, err := kubeconfig.GetNonInteractiveClientConfig(KubeConfig).ClientConfig()
	if err != nil {
		logrus.Fatalf("failed to find kubeconfig: %v", err)
	}

	restKubeConfig.RateLimiter = ratelimit.None

	restmapper, err := mapper.New(restKubeConfig)
	if err != nil {
		logrus.Fatalf("Error building rest mapper: %s", err.Error())
	}

	backups, err := resources.NewFactoryFromConfig(restKubeConfig)
	if err != nil {
		logrus.Fatalf("Error building sample controllers: %s", err.Error())
	}

	core, err := v1core.NewFactoryFromConfig(restKubeConfig)
	if err != nil {
		logrus.Fatalf("Error building sample controllers: %s", err.Error())
	}

	clientSet, err := clientset.NewForConfig(restKubeConfig)
	if err != nil {
		logrus.Fatalf("Error getting clientSet: %s", err.Error())
	}

	dynamicInterace, err := dynamic.NewForConfig(restKubeConfig)
	if err != nil {
		logrus.Fatalf("Error generating dynamic client: %s", err.Error())
	}
	sharedClientFactory, err := lasso.NewSharedClientFactoryForConfig(restKubeConfig)
	if err != nil {
		logrus.Fatalf("Error generating shared client factory: %s", err.Error())
	}

	// we should decide and set this path, and on top accept it from the user
	if DefaultBackupStorageLocation == "" {
		logrus.Infof("No temporary backup location provided, creating a new default in the temp dir")
		DefaultBackupStorageLocation = filepath.Join(os.TempDir(), "defaultbackuplocation")
		// create dir using ioutil
		_, err := os.Stat(DefaultBackupStorageLocation)
		if os.IsNotExist(err) {
			err = os.Mkdir(filepath.Join(os.TempDir(), "defaultbackuplocation"), os.ModePerm)
			if err != nil {
				logrus.Errorf("Error setting default location")
			}
		}
	} else {
		// This path should exist on the node on which the backup-restore-operator pods are running
		logrus.Infof("Default location for storing backups is set to %v", DefaultBackupStorageLocation)
		logrus.Infof("The following path must exist on the node %v", DefaultBackupStorageLocation)
	}

	util.ChartNamespace = ChartNamespace
	logrus.Infof("Secrets containing encryption config files must be stored in the namespace %v", ChartNamespace)

	backup.Register(ctx, backups.Resources().V1().Backup(), backups.Resources().V1().ResourceSet(),
		core.Core().V1().Secret(),
		core.Core().V1().Namespace(),
		clientSet, dynamicInterace, DefaultBackupStorageLocation)
	restore.Register(ctx, backups.Resources().V1().Restore(), backups.Resources().V1().Backup(),
		core.Core().V1().Secret(), clientSet, dynamicInterace, sharedClientFactory, restmapper)
	backup.StartBackupRetentionCheckDaemon(ctx, backups.Resources().V1().Backup(), core.Core().V1().Namespace(), dynamicInterace, "", DefaultBackupStorageLocation)

	if err := start.All(ctx, 2, backups); err != nil {
		logrus.Fatalf("Error starting: %s", err.Error())
	}

	<-ctx.Done()
}
