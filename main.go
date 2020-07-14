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
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/dynamic"
	"os"

	"github.com/mrajashree/backup/pkg/generated/controllers/backupper.cattle.io"
	"github.com/rancher/wrangler/pkg/kubeconfig"
	"github.com/rancher/wrangler/pkg/signals"
	"github.com/rancher/wrangler/pkg/start"
	//"github.com/rancher/wrangler/pkg/start"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

var (
	Version    = "v0.0.0-dev"
	GitCommit  = "HEAD"
	KubeConfig string
)

func main() {
	app := cli.NewApp()
	app.Name = "testy"
	app.Version = fmt.Sprintf("%s (%s)", Version, GitCommit)
	app.Usage = "testy needs help!"
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:        "kubeconfig",
			EnvVar:      "KUBECONFIG",
			Destination: &KubeConfig,
		},
	}
	app.Action = run

	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}

func run(c *cli.Context) {
	flag.Parse()

	logrus.Info("Starting controller")
	ctx := signals.SetupSignalHandler(context.Background())

	kubeConfig, err := kubeconfig.GetNonInteractiveClientConfig(KubeConfig).ClientConfig()
	if err != nil {
		logrus.Fatalf("failed to find kubeconfig: %v", err)
	}

	backups, err := backupper.NewFactoryFromConfig(kubeConfig)
	if err != nil {
		logrus.Fatalf("Error building sample controllers: %s", err.Error())
	}

	clientSet, err := clientset.NewForConfig(kubeConfig)
	if err != nil {
		logrus.Fatalf("Error getting clientSet: %s", err.Error())
	}

	dynamicInterace, err := dynamic.NewForConfig(kubeConfig)
	if err != nil {
		logrus.Fatalf("Error generating dynamic client: %s", err.Error())
	}

	backup.Register(ctx, backups.Backupper().V1().Backup(), backups.Backupper().V1().BackupTemplate(), clientSet, dynamicInterace)
	restore.Register(ctx, backups.Backupper().V1().Restore(), backups.Backupper().V1().Backup(), clientSet, dynamicInterace)

	if err := start.All(ctx, 2, backups); err != nil {
		logrus.Fatalf("Error starting: %s", err.Error())
	}

	<-ctx.Done()
}
