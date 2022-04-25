//go:generate go run pkg/codegen/cleanup/main.go
//go:generate /bin/rm -rf pkg/generated
//go:generate go run pkg/codegen/main.go

package main

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/backup-restore-operator/pkg/controllers/backup"
	"github.com/rancher/backup-restore-operator/pkg/controllers/restore"
	"github.com/rancher/backup-restore-operator/pkg/generated/controllers/resources.cattle.io"
	"github.com/rancher/backup-restore-operator/pkg/util"
	lasso "github.com/rancher/lasso/pkg/client"
	"github.com/rancher/lasso/pkg/mapper"
	v1core "github.com/rancher/wrangler/pkg/generated/controllers/core"
	"github.com/rancher/wrangler/pkg/kubeconfig"
	"github.com/rancher/wrangler/pkg/ratelimit"
	"github.com/rancher/wrangler/pkg/signals"
	"github.com/rancher/wrangler/pkg/start"
	"github.com/sirupsen/logrus"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	LogFormat = "2006/01/02 15:04:05"
)

var (
	Version                         = "v0.0.0-dev"
	GitCommit                       = "HEAD"
	LocalBackupStorageLocation      = "/var/lib/backups" // local within the pod, this is the mountPath for PVC
	KubeConfig                      string
	OperatorPVEnabled               string
	OperatorS3BackupStorageLocation string
	ChartNamespace                  string
	Debug                           bool
	Trace                           bool
)

type objectStore struct {
	Endpoint                  string `json:"endpoint"`
	EndpointCA                string `json:"endpointCA"`
	InsecureTLSSkipVerify     string `json:"insecureTLSSkipVerify"`
	CredentialSecretName      string `json:"credentialSecretName"`
	CredentialSecretNamespace string `json:"credentialSecretNamespace"`
	BucketName                string `json:"bucketName"`
	Region                    string `json:"region"`
	Folder                    string `json:"folder"`
}

func init() {
	flag.StringVar(&KubeConfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.BoolVar(&Debug, "debug", false, "Enable debug logging.")
	flag.BoolVar(&Trace, "trace", false, "Enable trace logging.")

	flag.Parse()
	OperatorPVEnabled = os.Getenv("DEFAULT_PERSISTENCE_ENABLED")
	OperatorS3BackupStorageLocation = os.Getenv("DEFAULT_S3_BACKUP_STORAGE_LOCATION")
	ChartNamespace = os.Getenv("CHART_NAMESPACE")
}

func main() {
	var defaultS3 *v1.S3ObjectStore
	var objStoreWithStrSkipVerify *objectStore
	var defaultMountPath string
	logrus.SetFormatter(&logrus.TextFormatter{FullTimestamp: true, ForceColors: true, TimestampFormat: LogFormat})
	if Debug {
		logrus.SetLevel(logrus.DebugLevel)
		logrus.Debugf("Loglevel set to [%v]", logrus.DebugLevel)
	}
	if Trace {
		logrus.SetLevel(logrus.TraceLevel)
		logrus.Tracef("Loglevel set to [%v]", logrus.TraceLevel)
	}

	logrus.Infof("Starting backup-restore controller version %s (%s)", Version, GitCommit)
	ctx := signals.SetupSignalContext()
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
		logrus.Fatalf("Error building backups sample controllers: %s", err.Error())
	}

	core, err := v1core.NewFactoryFromConfig(restKubeConfig)
	if err != nil {
		logrus.Fatalf("Error building core sample controllers: %s", err.Error())
	}

	clientSet, err := clientset.NewForConfig(restKubeConfig)
	if err != nil {
		logrus.Fatalf("Error getting clientSet: %s", err.Error())
	}

	k8sclient, err := kubernetes.NewForConfig(restKubeConfig)
	if err != nil {
		logrus.Fatalf("Error getting kubernetes client: %s", err.Error())
	}

	dynamicInterface, err := dynamic.NewForConfig(restKubeConfig)
	if err != nil {
		logrus.Fatalf("Error generating dynamic client: %s", err.Error())
	}
	sharedClientFactory, err := lasso.NewSharedClientFactoryForConfig(restKubeConfig)
	if err != nil {
		logrus.Fatalf("Error generating shared client factory: %s", err.Error())
	}

	if OperatorPVEnabled != "" && OperatorS3BackupStorageLocation != "" {
		logrus.Fatal("Cannot configure PVC and S3 both as default backup storage locations")
	}
	if OperatorPVEnabled == "" && OperatorS3BackupStorageLocation == "" {
		// when neither PVC nor s3 are provided, for dev mode, backups will be stored at /backups
		if dm := os.Getenv("CATTLE_DEV_MODE"); dm != "" {
			if dir, err := os.Getwd(); err == nil {
				dmPath := filepath.Join(dir, "backups")
				err := os.MkdirAll(dmPath, 0700)
				if err != nil {
					logrus.Fatalf("Error setting default location %v: %v", dmPath, err)
				}
				logrus.Infof("No temporary backup location provided, saving backups at %v", dmPath)
				defaultMountPath = dmPath
			}
		} else {
			// else, this log tells user that each backup needs to contain StorageLocation details
			logrus.Infof("No PVC or S3 details provided for storing backups by default. User must specify storageLocation" +
				" on each Backup CR")
		}
	} else if OperatorPVEnabled != "" {
		defaultMountPath = LocalBackupStorageLocation
	} else if OperatorS3BackupStorageLocation != "" {
		// read the secret from chart's namespace, with OperatorS3BackupStorageLocation as the name
		s3Secret, err := core.Core().V1().Secret().Get(ChartNamespace, OperatorS3BackupStorageLocation, k8sv1.GetOptions{})
		if err != nil {
			logrus.Fatalf("Error getting default s3 details %v: %v", OperatorS3BackupStorageLocation, err)
		}
		secStringData := make(map[string]interface{})
		for key, val := range s3Secret.Data {
			secStringData[key] = string(val)
		}
		secretData, err := json.Marshal(secStringData)
		if err != nil {
			logrus.Fatalf("Error marshaling s3 details secret: %v", err)
		}
		if err := json.Unmarshal(secretData, &objStoreWithStrSkipVerify); err != nil {
			logrus.Fatalf("Error unmarshaling s3 details secret: %v", err)
		}

		defaultS3 = &v1.S3ObjectStore{
			Endpoint:                  objStoreWithStrSkipVerify.Endpoint,
			EndpointCA:                objStoreWithStrSkipVerify.EndpointCA,
			CredentialSecretName:      objStoreWithStrSkipVerify.CredentialSecretName,
			CredentialSecretNamespace: objStoreWithStrSkipVerify.CredentialSecretNamespace,
			BucketName:                objStoreWithStrSkipVerify.BucketName,
			Region:                    objStoreWithStrSkipVerify.Region,
			Folder:                    objStoreWithStrSkipVerify.Folder,
		}
		if objStoreWithStrSkipVerify.InsecureTLSSkipVerify == "true" {
			defaultS3.InsecureTLSSkipVerify = true
		}
	}

	util.ChartNamespace = ChartNamespace
	logrus.Infof("Secrets containing encryption config files must be stored in the namespace %v", ChartNamespace)

	backup.Register(ctx, backups.Resources().V1().Backup(),
		backups.Resources().V1().ResourceSet(),
		core.Core().V1().Secret(),
		core.Core().V1().Namespace(),
		clientSet, dynamicInterface, defaultMountPath, defaultS3)
	restore.Register(ctx, backups.Resources().V1().Restore(),
		backups.Resources().V1().Backup(),
		core.Core().V1().Secret(),
		k8sclient.CoordinationV1().Leases(ChartNamespace),
		clientSet, dynamicInterface, sharedClientFactory, restmapper, defaultMountPath, defaultS3)

	if err := start.All(ctx, 2, backups); err != nil {
		logrus.Fatalf("Error starting: %s", err.Error())
	}

	<-ctx.Done()
}
