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
	"github.com/rancher/backup-restore-operator/pkg/monitoring"
	"github.com/rancher/backup-restore-operator/pkg/objectstore"
	"github.com/rancher/backup-restore-operator/pkg/util"
	lasso "github.com/rancher/lasso/pkg/client"
	"github.com/rancher/lasso/pkg/mapper"
	v1core "github.com/rancher/wrangler/v3/pkg/generated/controllers/core"
	"github.com/rancher/wrangler/v3/pkg/ratelimit"
	"github.com/rancher/wrangler/v3/pkg/start"
	"github.com/sirupsen/logrus"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/api/errors"
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
	MetricsServerEnabled            bool
	MetricsPort                     int
	MetricsIntervalSeconds          int
	OperatorS3BackupStorageLocation string
	ChartNamespace                  string
	LocalDriverPath                 string
	LocalEncryptionProviderLocation string
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

	if o.MetricsServerEnabled && o.MetricsPort <= 0 {
		return fmt.Errorf("invalid port metrics port : %d", o.MetricsPort)
	}

	if o.MetricsServerEnabled && o.MetricsIntervalSeconds <= 0 {
		return fmt.Errorf("invalid metrics interval : %d", o.MetricsIntervalSeconds)
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

func (o *RunOptions) shouldRunMetricsServer() bool {
	return o.MetricsServerEnabled
}

type ControllerOptions struct {
	mapper                  meta.RESTMapper
	clientSet               *clientset.Clientset
	k8sClient               *kubernetes.Clientset
	backupFactory           *resources.Factory
	core                    *v1core.Factory
	dynamic                 *dynamic.DynamicClient
	dynamicHasImpersonation bool
	sharedFactory           lasso.SharedClientFactory
}

func setup(ctx context.Context, kubeconfig *rest.Config) (ControllerOptions, error) {
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

	sharedClientFactory, err := lasso.NewSharedClientFactoryForConfig(kubeconfig)
	if err != nil {
		return ControllerOptions{}, fmt.Errorf("error generating shared client factory: %s", err.Error())
	}

	webhookSA, err := k8sclient.CoreV1().ServiceAccounts("cattle-system").Get(ctx, "rancher-webhook-sudo", metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return ControllerOptions{}, fmt.Errorf("error validating existence of rancher-webhook-sudo service account: %s", err.Error())
	}

	dynamicHasImpersonation := false
	if webhookSA != nil {
		logrus.Info("initializing dynamic client with webhook impersonation")
		kubeconfig.Impersonate = webhookImpersonation()
		dynamicHasImpersonation = true
	} else {
		logrus.Info("initializing dynamic client without webhook impersonation")
	}

	dynamicInterface, err := dynamic.NewForConfig(kubeconfig)
	if err != nil {
		return ControllerOptions{}, fmt.Errorf("error generating dynamic client: %s", err.Error())
	}
	return ControllerOptions{
		mapper:                  restmapper,
		clientSet:               clientSet,
		k8sClient:               k8sclient,
		backupFactory:           backups,
		core:                    coreF,
		dynamic:                 dynamicInterface,
		dynamicHasImpersonation: dynamicHasImpersonation,
		sharedFactory:           sharedClientFactory,
	}, nil
}

// WebhookImpersonation returns a ImpersonationConfig that can be used for impersonating the webhook's sudo account and bypass validation.
func webhookImpersonation() rest.ImpersonationConfig {
	return rest.ImpersonationConfig{
		UserName: "system:serviceaccount:cattle-system:rancher-webhook-sudo",
		Groups:   []string{"system:masters"},
	}
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
		ClientConfig:              objStoreWithStrSkipVerify.ClientConfig,
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

	var metricsServerEnabled bool

	var defaultS3 *backupv1.S3ObjectStore
	var defaultMountPath string
	if err := options.Validate(); err != nil {
		return err
	}

	kubeconfig.RateLimiter = ratelimit.None

	c, err := setup(ctx, kubeconfig)
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

	if options.shouldRunMetricsServer() {
		logrus.Info("Starting metrics server")
		go monitoring.InitMetricsServer(options.MetricsPort)

		logrus.Info("Starting metadata metrics loop")
		go monitoring.StartBackupMetricsCollection(c.backupFactory.Resources().V1().Backup(), options.MetricsIntervalSeconds)
		go monitoring.StartRestoreMetricsCollection(c.backupFactory.Resources().V1().Restore(), options.MetricsIntervalSeconds)

		metricsServerEnabled = options.shouldRunMetricsServer()
	}

	logrus.Infof("Secrets containing encryption config files must be stored in the namespace %v", options.ChartNamespace)

	encryptionProviderLocation := options.LocalEncryptionProviderLocation

	backup.Register(ctx,
		c.backupFactory.Resources().V1().Backup(),
		c.backupFactory.Resources().V1().ResourceSet(),
		c.core.Core().V1().Secret(),
		c.core.Core().V1().Namespace(),
		c.clientSet,
		c.dynamic,
		defaultMountPath,
		defaultS3,
		metricsServerEnabled,
		encryptionProviderLocation,
	)
	restore.Register(ctx,
		c.backupFactory.Resources().V1().Restore(),
		c.backupFactory.Resources().V1().Backup(),
		c.core.Core().V1().Secret(),
		c.k8sClient.CoordinationV1().Leases(options.ChartNamespace),
		c.clientSet,
		c.dynamic,
		c.dynamicHasImpersonation,
		kubeconfig,
		c.sharedFactory,
		c.mapper,
		defaultMountPath,
		defaultS3,
		metricsServerEnabled,
		encryptionProviderLocation,
	)

	if err := start.All(ctx, 2, c.backupFactory); err != nil {
		logrus.Fatalf("Error starting: %s", err.Error())
	}

	<-ctx.Done()
	return nil
}
