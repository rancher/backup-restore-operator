package encryptionconfig

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/rancher/backup-restore-operator/pkg/util"
	v1 "github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"

	"github.com/sirupsen/logrus"
	v1core "k8s.io/api/core/v1"
	v2 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sEncryptionconfig "k8s.io/apiserver/pkg/server/options/encryptionconfig"
)

const EncryptionProviderConfigKey = "encryption-provider-config.yaml"

func GetEncryptionConfigSecret(secrets v1.SecretController, encryptionConfigSecretName string) (*v1core.Secret, error) {
	// EncryptionConfig secret ns is hardcoded to ns of controller in chart's ns
	// kubectl create secret generic test-encryptionconfig --from-file=./encryption-provider-config.yaml
	logrus.WithFields(logrus.Fields{"get_chart_namespace": util.GetChartNamespace()}).Info("Retrieving encryption configuration from chart namespace")
	encryptionConfigSecret, err := secrets.Get(util.GetChartNamespace(), encryptionConfigSecretName, v2.GetOptions{})
	if err != nil {
		return nil, err
	}

	return encryptionConfigSecret, nil
}

func GetEncryptionTransformersFromSecret(ctx context.Context, encryptionConfigSecret *v1core.Secret, encryptionProviderPath string) (k8sEncryptionconfig.StaticTransformers, error) {
	err := prepareEncryptionConfigSecretTempConfig(encryptionConfigSecret, encryptionProviderPath)
	// we defer file removal till here to ensure it's around for all of PrepareEncryptionTransformersFromConfig

	fullEncryptionProviderPath := path.Join(encryptionProviderPath, EncryptionProviderConfigKey)
	defer os.Remove(fullEncryptionProviderPath)
	if err != nil {
		return nil, err
	}
	return PrepareEncryptionTransformersFromConfig(ctx, fullEncryptionProviderPath)
}

func prepareEncryptionConfigSecretTempConfig(encryptionConfigSecret *v1core.Secret, encryptionProviderPath string) error {
	encryptionConfigBytes, ok := encryptionConfigSecret.Data[EncryptionProviderConfigKey]
	if !ok {
		return fmt.Errorf("no encryptionConfig provided")
	}

	fullEncryptionProviderPath := path.Join(encryptionProviderPath, EncryptionProviderConfigKey)
	err := os.WriteFile(fullEncryptionProviderPath, encryptionConfigBytes, os.ModePerm)
	if err != nil {
		return err
	}

	return nil
}

func PrepareEncryptionTransformersFromConfig(ctx context.Context, fullEncryptionProviderPath string) (k8sEncryptionconfig.StaticTransformers, error) {
	apiServerID := ""
	encryptionConfig, err := k8sEncryptionconfig.LoadEncryptionConfig(ctx, fullEncryptionProviderPath, false, apiServerID)
	if err != nil {
		return nil, err
	}
	return encryptionConfig.Transformers, nil
}
