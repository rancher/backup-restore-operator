package util

import (
	"bytes"
	"fmt"
	"reflect"

	v1core "github.com/rancher/wrangler/pkg/generated/controllers/core/v1"
	"github.com/sirupsen/logrus"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/server/options/encryptionconfig"
	"k8s.io/apiserver/pkg/storage/value"
)

const (
	WorkerThreads               = 25
	S3Backup                    = "S3"
	PVBackup                    = "PV"
	encryptionProviderConfigKey = "encryption-provider-config.yaml"
)

var ChartNamespace string

func GetEncryptionTransformers(encryptionConfigSecretName string, secrets v1core.SecretController) (map[schema.GroupResource]value.Transformer, error) {
	var transformerMap map[schema.GroupResource]value.Transformer
	// EncryptionConfig secret ns is hardcoded to ns of controller in chart's ns
	// kubectl create secret generic test-encryptionconfig --from-file=./encryption-provider-config.yaml
	logrus.Infof("Get encryption config from namespace %v", ChartNamespace)
	encryptionConfigSecret, err := secrets.Get(ChartNamespace, encryptionConfigSecretName, k8sv1.GetOptions{})
	if err != nil {
		return transformerMap, err
	}
	encryptionConfigBytes, ok := encryptionConfigSecret.Data[encryptionProviderConfigKey]
	if !ok {
		return transformerMap, fmt.Errorf("no encryptionConfig provided")
	}
	return encryptionconfig.ParseEncryptionConfiguration(bytes.NewReader(encryptionConfigBytes))
}

func GetObjectQueue(l interface{}, capacity int) chan interface{} {
	s := reflect.ValueOf(l)
	c := make(chan interface{}, capacity)

	for i := 0; i < s.Len(); i++ {
		c <- s.Index(i).Interface()
	}
	return c
}

func ErrList(e []error) error {
	if len(e) > 0 {
		return fmt.Errorf("%v", e)
	}
	return nil
}
