package util

import (
	"context"
	"fmt"
	"os"
	"reflect"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsClientSetv1 "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"

	v1core "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"
	"github.com/sirupsen/logrus"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/server/options/encryptionconfig"
	"k8s.io/apiserver/pkg/storage/value"
	"k8s.io/apiserver/pkg/storage/value/encrypt/identity"
)

const (
	WorkerThreads               = 25
	S3Backup                    = "S3"
	PVBackup                    = "PV"
	EncryptionProviderConfigKey = "encryption-provider-config.yaml"
)

var (
	chartNamespace string
	devMode        bool
)

var (
	initNs  = Initializer{}
	initDev = Initializer{}
)

func SetDevMode(enabled bool) {
	initDev.InitOnce(func() {
		devMode = enabled
	})
}

func DevMode() bool {
	initDev.WaitForInit()
	return devMode
}

func DevModeContext(ctx context.Context) bool {
	err := initDev.WaitForInitContext(ctx)
	if err != nil {
		return false
	}
	return devMode
}

func GetChartNamespaceContext(ctx context.Context) (string, error) {
	if err := initNs.WaitForInitContext(ctx); err != nil {
		return "", err
	}
	return chartNamespace, nil
}

func GetChartNamespace() string {
	initNs.WaitForInit()
	return chartNamespace
}

func SetChartNamespace(ns string) {
	initNs.InitOnce(func() {
		chartNamespace = ns
	})
}

func GetEncryptionTransformersFromSecret(encryptionConfigSecretName string, secrets v1core.SecretController) (map[schema.GroupResource]value.Transformer, error) {
	// EncryptionConfig secret ns is hardcoded to ns of controller in chart's ns
	// kubectl create secret generic test-encryptionconfig --from-file=./encryption-provider-config.yaml
	logrus.Infof("Get encryption config from namespace %v", GetChartNamespace())
	encryptionConfigSecret, err := secrets.Get(GetChartNamespace(), encryptionConfigSecretName, k8sv1.GetOptions{})
	if err != nil {
		return nil, err
	}
	encryptionConfigBytes, ok := encryptionConfigSecret.Data[EncryptionProviderConfigKey]
	if !ok {
		return nil, fmt.Errorf("no encryptionConfig provided")
	}
	err = os.WriteFile(EncryptionProviderConfigKey, encryptionConfigBytes, os.ModePerm)
	defer os.Remove(EncryptionProviderConfigKey)

	if err != nil {
		return nil, err
	}
	return PrepareEncryptionTransformersFromConfig(context.Background(), EncryptionProviderConfigKey)
}

func PrepareEncryptionTransformersFromConfig(ctx context.Context, encryptionProviderPath string) (map[schema.GroupResource]value.Transformer, error) {
	apiServerID := ""
	encryptionConfig, err := encryptionconfig.LoadEncryptionConfig(ctx, encryptionProviderPath, false, apiServerID)
	if err != nil {
		return nil, err
	}
	return encryptionConfig.Transformers, nil
}

func GetObjectQueue(l interface{}, capacity int) chan interface{} {
	s := reflect.ValueOf(l)
	c := make(chan interface{}, capacity)

	for i := 0; i < s.Len(); i++ {
		c <- s.Index(i).Interface()
	}
	return c
}

func IsDefaultEncryptionTransformer(transformer value.Transformer) bool {
	return transformer == identity.NewEncryptCheckTransformer()
}

func ErrList(e []error) error {
	if len(e) > 0 {
		return fmt.Errorf("%v", e)
	}
	return nil
}

func FetchClusterUID(namespaces v1core.NamespaceController) (string, error) {
	kubesystemNamespace, err := namespaces.Get("kube-system", k8sv1.GetOptions{})
	if err != nil {
		return "", err
	}

	return string(kubesystemNamespace.UID), nil
}

// Define the GroupVersionResource for CRDs
var crdGVR = schema.GroupVersionResource{
	Group:    "apiextensions.k8s.io",
	Version:  "v1",
	Resource: "customresourcedefinitions",
}

func getCRDDefinition(dynamicClient apiextensionsClientSetv1.ApiextensionsV1Interface, crdName string) (*apiextensionsv1.CustomResourceDefinition, error) {
	crd, err := dynamicClient.CustomResourceDefinitions().Get(context.TODO(), crdName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return crd, nil
}

func VerifyBackupCrdHasClusterStatus(client apiextensionsClientSetv1.ApiextensionsV1Interface) bool {
	crdName := "backups.resources.cattle.io"

	crd, err := getCRDDefinition(client, crdName)
	if err != nil {
		logrus.Infof("Error fetching CRD: %v", err)
		return false
	}

	// Inspect the status schema, for example
	_, found := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"].Properties["originCluster"]
	if found {
		logrus.Debugf("Status schema contains `originCluster` on CRD `%s`.\n", crdName)
		return true
	}

	logrus.Debugf("`originCluster` not found on status schema for CRD `%s`.\n", crdName)
	return false
}
