package encryptionconfig

import (
	"context"
	"fmt"
	"os"

	"github.com/rancher/backup-restore-operator/pkg/util"
	"github.com/rancher/wrangler/v3/pkg/generated/controllers/core/v1"

	"github.com/sirupsen/logrus"
	v1core "k8s.io/api/core/v1"
	v2 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sEncryptionconfig "k8s.io/apiserver/pkg/server/options/encryptionconfig"
	storagevalue "k8s.io/apiserver/pkg/storage/value"
	"k8s.io/apiserver/pkg/storage/value/encrypt/identity"
)

const EncryptionProviderConfigKey = "encryption-provider-config.yaml"

func GetEncryptionTransformersFromSecret(ctx context.Context, encryptionConfigSecretName string, secrets v1.SecretController) (k8sEncryptionconfig.StaticTransformers, error) {
	// EncryptionConfig secret ns is hardcoded to ns of controller in chart's ns
	// kubectl create secret generic test-encryptionconfig --from-file=./encryption-provider-config.yaml
	logrus.Infof("Get encryption config from namespace %v", util.GetChartNamespace())
	encryptionConfigSecret, err := secrets.Get(util.GetChartNamespace(), encryptionConfigSecretName, v2.GetOptions{})
	if err != nil {
		return nil, err
	}
	err = PrepareEncryptionConfigSecretTempConfig(encryptionConfigSecret)
	if err != nil {
		return nil, err
	}
	return PrepareEncryptionTransformersFromConfig(ctx, EncryptionProviderConfigKey)
}

func PrepareEncryptionConfigSecretTempConfig(encryptionConfigSecret *v1core.Secret) error {
	encryptionConfigBytes, ok := encryptionConfigSecret.Data[EncryptionProviderConfigKey]
	if !ok {
		return fmt.Errorf("no encryptionConfig provided")
	}
	err := os.WriteFile(EncryptionProviderConfigKey, encryptionConfigBytes, os.ModePerm)
	defer os.Remove(EncryptionProviderConfigKey)
	if err != nil {
		return err
	}

	return nil
}

func PrepareEncryptionTransformersFromConfig(ctx context.Context, encryptionProviderPath string) (k8sEncryptionconfig.StaticTransformers, error) {
	apiServerID := ""
	encryptionConfig, err := k8sEncryptionconfig.LoadEncryptionConfig(ctx, encryptionProviderPath, false, apiServerID)
	if err != nil {
		return nil, err
	}
	return encryptionConfig.Transformers, nil
}

func IsDefaultEncryptionTransformer(transformer storagevalue.Transformer) bool {
	return transformer == identity.NewEncryptCheckTransformer()
}
