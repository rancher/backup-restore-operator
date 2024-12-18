package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	backupv1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/backup-restore-operator/pkg/controllers/backup"
	"github.com/rancher/backup-restore-operator/pkg/controllers/restore"
	"github.com/rancher/backup-restore-operator/pkg/generated/controllers/resources.cattle.io"
	"github.com/rancher/backup-restore-operator/pkg/objectstore"
	"github.com/rancher/backup-restore-operator/pkg/util"
	lasso "github.com/rancher/lasso/pkg/client"
	"github.com/rancher/lasso/pkg/mapper"
	v1core "github.com/rancher/wrangler/v3/pkg/generated/controllers/core"
	"github.com/rancher/wrangler/v3/pkg/ratelimit"
	"github.com/rancher/wrangler/v3/pkg/start"
	"github.com/sirupsen/logrus"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	LocalBackupStorageLocation = "/var/lib/backups" // local within the pod, this is the mountPath for PVC
)

type RunOptions struct {
	OperatorPVCEnabled              bool
	OperatorS3BackupStorageLocation string
	ChartNamespace                  string
	LocalDriverPath                 string
}

func (o *RunOptions) Validate() error {
	if o.OperatorPVCEnabled && o.OperatorS3BackupStorageLocation != "" {
		return fmt.Errorf("cannot configure PVC and S3 both as default backup storage locations")
	}
	// case where user has not explicitly enabled any details and we are not in "dev mode"
	if o.OperatorPVCEnabled && o.OperatorS3BackupStorageLocation == "" && !util.DevMode() {
		logrus.Infof("No PVC or S3 details provided for storing backups by default. User must specify storageLocation" +
			" on each Backup CR")
	}
	return nil
}

func (o *RunOptions) shouldUseLocalDriver() bool {
	return !o.OperatorPVCEnabled && util.DevMode()
}

func (o *RunOptions) shouldUsePVC() bool {
	return o.OperatorPVCEnabled
}

func (o *RunOptions) shouldUseS3() bool {
	return o.OperatorS3BackupStorageLocation != ""
}

type ControllerOptions struct {
	mapper        meta.RESTMapper
	clientSet     *clientset.Clientset
	k8sClient     *kubernetes.Clientset
	backupFactory *resources.Factory
	core          *v1core.Factory
	dynamic       *dynamic.DynamicClient
	sharedFactory lasso.SharedClientFactory
}

func setup(kubeconfig *rest.Config) (ControllerOptions, error) {
	restmapper, err := mapper.New(kubeconfig)
	if err != nil {
		return ControllerOptions{}, fmt.Errorf("error building rest mapper: %s", err.Error())
	}

	backups, err := resources.NewFactoryFromConfig(kubeconfig)
	if err != nil {
		return ControllerOptions{}, fmt.Errorf("error building backups sample controllers: %s", err.Error())
	}

	coreF, err := v1core.NewFactoryFromConfig(kubeconfig)
	if err != nil {
		return ControllerOptions{}, fmt.Errorf("error building core sample controllers: %s", err.Error())
	}

	clientSet, err := clientset.NewForConfig(kubeconfig)
	if err != nil {
		return ControllerOptions{}, fmt.Errorf("error getting clientSet: %s", err.Error())
	}

	k8sclient, err := kubernetes.NewForConfig(kubeconfig)
	if err != nil {
		return ControllerOptions{}, fmt.Errorf("error getting kubernetes client: %s", err.Error())
	}

	dynamicInterface, err := dynamic.NewForConfig(kubeconfig)
	if err != nil {
		return ControllerOptions{}, fmt.Errorf("error generating dynamic client: %s", err.Error())
	}
	sharedClientFactory, err := lasso.NewSharedClientFactoryForConfig(kubeconfig)
	if err != nil {
		return ControllerOptions{}, fmt.Errorf("error generating shared client factory: %s", err.Error())
	}
	return ControllerOptions{
		mapper:        restmapper,
		clientSet:     clientSet,
		k8sClient:     k8sclient,
		backupFactory: backups,
		core:          coreF,
		dynamic:       dynamicInterface,
		sharedFactory: sharedClientFactory,
	}, nil
}

func FetchDefaultS3Configuration(options RunOptions, core *v1core.Factory) (*backupv1.S3ObjectStore, error) {
	s3Secret, err := core.Core().V1().Secret().Get(options.ChartNamespace, options.OperatorS3BackupStorageLocation, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	stringData := make(map[string]interface{})
	for key, val := range s3Secret.Data {
		stringData[key] = string(val)
	}

	secretData, err := json.Marshal(stringData)
	if err != nil {
		return nil, fmt.Errorf("error marshalling s3 details secret : %s", err)
	}

	var objStoreWithStrSkipVerify *objectstore.ObjectStore
	if err := json.Unmarshal(secretData, &objStoreWithStrSkipVerify); err != nil {
		return nil, fmt.Errorf("error Unmarshalling s3 details secret : %s", err)
	}

	defaultS3 := &backupv1.S3ObjectStore{
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

	return defaultS3, nil
}

// Run is blocking
func Run(
	ctx context.Context,
	kubeconfig *rest.Config,
	options RunOptions,
) error {
	util.SetChartNamespace(options.ChartNamespace)

	var defaultS3 *backupv1.S3ObjectStore
	var defaultMountPath string
	if err := options.Validate(); err != nil {
		return err
	}

	kubeconfig.RateLimiter = ratelimit.None

	c, err := setup(kubeconfig)
	if err != nil {
		return err
	}

	if options.shouldUseLocalDriver() {
		if options.LocalDriverPath == "" {
			if dir, err := os.Getwd(); err == nil {
				dmPath := filepath.Join(dir, "backups")
				err := os.MkdirAll(dmPath, 0700)
				if err != nil {
					logrus.Fatalf("Error setting default location %v: %v", dmPath, err)
				}
				logrus.Infof("No temporary backup location provided, saving backups at %v", dmPath)
				defaultMountPath = dmPath
			}
		}
		// TODO : add a branch handling local driver path override for testing while in dev mode
	} else if options.shouldUsePVC() {
		defaultMountPath = LocalBackupStorageLocation
	} else if options.shouldUseS3() {
		s3details, err := FetchDefaultS3Configuration(options, c.core)
		if err != nil {
			return err
		}
		defaultS3 = s3details
	}

	logrus.Infof("Secrets containing encryption config files must be stored in the namespace %v", options.ChartNamespace)

	backup.Register(ctx, c.backupFactory.Resources().V1().Backup(),
		c.backupFactory.Resources().V1().ResourceSet(),
		c.core.Core().V1().Secret(),
		c.core.Core().V1().Namespace(),
		c.clientSet,
		c.dynamic,
		defaultMountPath,
		defaultS3,
	)
	restore.Register(ctx, c.backupFactory.Resources().V1().Restore(),
		c.backupFactory.Resources().V1().Backup(),
		c.core.Core().V1().Secret(),
		c.k8sClient.CoordinationV1().Leases(options.ChartNamespace),
		c.clientSet,
		c.dynamic,
		c.sharedFactory,
		c.mapper,
		defaultMountPath,
		defaultS3,
	)

	if err := start.All(ctx, 2, c.backupFactory); err != nil {
		logrus.Fatalf("Error starting: %s", err.Error())
	}

	<-ctx.Done()
	return nil
}
