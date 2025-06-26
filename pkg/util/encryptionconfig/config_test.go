package encryptionconfig

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value/encrypt/identity"
)

var serviceAccountGVR = schema.GroupVersionResource{
	Group:    "",
	Version:  "v1",
	Resource: "serviceaccounts",
}

var deploymentGVR = schema.GroupVersionResource{
	Group:    "apps",
	Version:  "v1",
	Resource: "deployments",
}

func TestGetEncryptionTransformersFromSecret_Basic(t *testing.T) {
	encryptionConfigFilepath := filepath.Join("testdata", "encryption-provider-config-wildcard.yaml")
	configBytes, _ := os.ReadFile(encryptionConfigFilepath)

	testSecret := v1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: "test-secret"},
		Data: map[string][]byte{
			EncryptionProviderConfigKey: configBytes,
		},
	}
	transformers, err := GetEncryptionTransformersFromSecret(
		context.Background(),
		&testSecret,
		EncryptionProviderConfigKey,
	)

	assert.Nil(t, err)
	assert.NotNil(t, transformers)
}

func TestPrepareEncryptionConfigSecretTempConfig_ValidSecretKeySillyData(t *testing.T) {
	sillyTestData := "Hello Rancher! Moo."
	testSecret := v1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: "test-secret"},
		Data: map[string][]byte{
			EncryptionProviderConfigKey: []byte(sillyTestData),
		},
	}
	err := prepareEncryptionConfigSecretTempConfig(&testSecret, EncryptionProviderConfigKey)
	defer os.Remove(EncryptionProviderConfigKey)
	// Assert that no error is returned
	assert.Nil(t, err)
	file, err := os.Open(EncryptionProviderConfigKey)
	if err != nil {
		t.FailNow()
	}

	// Read the file written by prepareEncryptionConfigSecretTempConfig
	actualBytes := make([]byte, 1024)
	n, err := file.Read(actualBytes)
	if err != nil {
		t.Fatal(err)
	}

	// Assert that the contents of the file match the expected encryption config value
	assert.Equal(t, sillyTestData, string(actualBytes[:n]))
}

func TestPrepareEncryptionConfigSecretTempConfig_EmptySecret(t *testing.T) {
	testSecret := v1.Secret{}
	err := prepareEncryptionConfigSecretTempConfig(&testSecret, EncryptionProviderConfigKey)
	assert.NotNil(t, err)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "no encryptionConfig provided")
}

func TestPrepareEncryptionConfigSecretTempConfig_IncorrectSecretKey(t *testing.T) {
	testSecret := v1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: "test-secret"},
		Data: map[string][]byte{
			"key": []byte("value"),
		},
	}
	err := prepareEncryptionConfigSecretTempConfig(&testSecret, EncryptionProviderConfigKey)
	assert.NotNil(t, err)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "no encryptionConfig provided")

}

func TestPrepareEncryptionTransformersFromConfig_Wildcard(t *testing.T) {
	encryptionConfigFilepath := filepath.Join("testdata", "encryption-provider-config-wildcard.yaml")
	transformers, err := PrepareEncryptionTransformersFromConfig(context.Background(), encryptionConfigFilepath)
	assert.Nil(t, err)

	assert.NotNil(t, transformers.TransformerForResource(serviceAccountGVR.GroupResource()))
	assert.NotNil(t, transformers.TransformerForResource(deploymentGVR.GroupResource()))
	assert.NotEqual(t, transformers.TransformerForResource(deploymentGVR.GroupResource()), identity.NewEncryptCheckTransformer())
}

func TestPrepareEncryptionTransformersFromConfig_ErrorsWithInvalidConfig(t *testing.T) {
	encryptionConfigFilepath := filepath.Join("testdata", "encryption-provider-config-invalid.yaml")
	nilTransformers, err := PrepareEncryptionTransformersFromConfig(context.Background(), encryptionConfigFilepath)
	assert.Nil(t, nilTransformers)
	assert.NotNil(t, err)

	assert.Error(t, err)
	assert.ErrorContains(t, err, "error while parsing file: error decoding encryption provider configuration file")
}
