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

const contextKey = "tmpConfigPath"
const EncryptionProviderConfigKey = "encryption-provider-config.yaml"

func GetEncryptionConfigSecret(secrets v1.SecretController, encryptionConfigSecretName string) (*v1core.Secret, error) {
	// EncryptionConfig secret ns is hardcoded to ns of controller in chart's ns
	// kubectl create secret generic test-encryptionconfig --from-file=./encryption-provider-config.yaml
	logrus.Infof("Get encryption config from namespace %v", util.GetChartNamespace())
	encryptionConfigSecret, err := secrets.Get(util.GetChartNamespace(), encryptionConfigSecretName, v2.GetOptions{})
	if err != nil {
		return nil, err
	}

	return encryptionConfigSecret, nil
}

func GetEncryptionTransformersFromSecret(ctx context.Context, encryptionConfigSecret *v1core.Secret) (k8sEncryptionconfig.StaticTransformers, error) {
	fileHandle, err := PrepareEncryptionConfigSecretTempConfig(encryptionConfigSecret)
	// we defer file removal till here to ensure it's around for all of PrepareEncryptionTransformersFromConfig
	defer os.Remove(EncryptionProviderConfigKey)
	if err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, contextKey, fileHandle.Name())
	return PrepareEncryptionTransformersFromConfig(ctx, EncryptionProviderConfigKey)
}

func PrepareEncryptionConfigSecretTempConfig(encryptionConfigSecret *v1core.Secret) (*os.File, error) {
	encryptionConfigBytes, ok := encryptionConfigSecret.Data[EncryptionProviderConfigKey]
	if !ok {
		return nil, fmt.Errorf("no encryptionConfig provided")
	}
	err := os.WriteFile(EncryptionProviderConfigKey, encryptionConfigBytes, os.ModePerm)
	if err != nil {
		return nil, err
	}

	// Open the file for reading (or other operations) and return the handle
	file, err := os.Open(EncryptionProviderConfigKey)
	if err != nil {
		return nil, err
	}

	return file, nil
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
