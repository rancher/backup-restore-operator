//go:generate go run pkg/codegen/cleanup/main.go
//go:generate /bin/rm -rf pkg/generated
//go:generate go run pkg/codegen/main.go

package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/mrajashree/backup/pkg/controllers/backup"
	"github.com/mrajashree/backup/pkg/controllers/restore"
	"github.com/mrajashree/backup/pkg/generated/controllers/backupper.cattle.io"
	lasso "github.com/rancher/lasso/pkg/client"
	"github.com/rancher/lasso/pkg/mapper"
	"github.com/rancher/wrangler/pkg/kubeconfig"
	"github.com/rancher/wrangler/pkg/ratelimit"
	"github.com/rancher/wrangler/pkg/signals"
	"github.com/rancher/wrangler/pkg/start"
	"github.com/sirupsen/logrus"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/dynamic"
)

var (
	Version    = "v0.0.0-dev"
	GitCommit  = "HEAD"
	KubeConfig string
)

func init() {
	flag.StringVar(&KubeConfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.Parse()
}

func main() {
	logrus.Info("Starting controller")
	ctx := signals.SetupSignalHandler(context.Background())
	fmt.Printf("kubeconfig: %v\n", KubeConfig)

	restKubeConfig, err := kubeconfig.GetNonInteractiveClientConfig(KubeConfig).ClientConfig()
	if err != nil {
		logrus.Fatalf("failed to find kubeconfig: %v", err)
	}

	restKubeConfig.RateLimiter = ratelimit.None

	restmapper, err := mapper.New(restKubeConfig)
	if err != nil {
		logrus.Fatalf("Error building rest mapper: %s", err.Error())
	}

	backups, err := backupper.NewFactoryFromConfig(restKubeConfig)
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
	backup.Register(ctx, backups.Backupper().V1().Backup(), backups.Backupper().V1().BackupTemplate(), backups.Backupper().V1().BackupEncryptionConfig(), clientSet, dynamicInterace)
	restore.Register(ctx, backups.Backupper().V1().Restore(), backups.Backupper().V1().Backup(), backups.Backupper().V1().BackupEncryptionConfig(), clientSet, dynamicInterace, sharedClientFactory, restmapper)

	if err := start.All(ctx, 2, backups); err != nil {
		logrus.Fatalf("Error starting: %s", err.Error())
	}

	<-ctx.Done()
}
