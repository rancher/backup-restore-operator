package encryptionconfig

import (
	"context"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
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
	fileHandle, err := PrepareEncryptionConfigSecretTempConfig(&testSecret)
	defer os.Remove(EncryptionProviderConfigKey)
	// Assert that no error is returned
	assert.Nil(t, err)

	// Read the file written by PrepareEncryptionConfigSecretTempConfig
	actualBytes := make([]byte, 1024)
	n, err := fileHandle.Read(actualBytes)
	if err != nil {
		t.Fatal(err)
	}

	// Assert that the contents of the file match the expected encryption config value
	assert.Equal(t, sillyTestData, string(actualBytes[:n]))
}

func TestPrepareEncryptionConfigSecretTempConfig_EmptySecret(t *testing.T) {
	testSecret := v1.Secret{}
	_, err := PrepareEncryptionConfigSecretTempConfig(&testSecret)
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
	_, err := PrepareEncryptionConfigSecretTempConfig(&testSecret)
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

func TestIsDefaultEncryptionTransformer_Wildcard(t *testing.T) {
	encryptionConfigFilepath := filepath.Join("testdata", "encryption-provider-config-wildcard.yaml")
	transformers, err := PrepareEncryptionTransformersFromConfig(context.Background(), encryptionConfigFilepath)
	assert.Nil(t, err)

	serviceAccountTransformer := transformers.TransformerForResource(serviceAccountGVR.GroupResource())
	deploymentTransformer := transformers.TransformerForResource(deploymentGVR.GroupResource())

	assert.False(t, IsDefaultEncryptionTransformer(serviceAccountTransformer))
	assert.False(t, IsDefaultEncryptionTransformer(deploymentTransformer))
}

func TestIsDefaultEncryptionTransformer_PartialWildcard(t *testing.T) {
	encryptionConfigFilepath := filepath.Join("testdata", "encryption-provider-config-partial-wildcard.yaml")
	ctx := context.WithValue(context.Background(), tempConfigPathKey, encryptionConfigFilepath)
	transformers, err := PrepareEncryptionTransformersFromConfig(ctx, encryptionConfigFilepath)
	assert.Nil(t, err)

	serviceAccountTransformer := transformers.TransformerForResource(serviceAccountGVR.GroupResource())
	deploymentTransformer := transformers.TransformerForResource(deploymentGVR.GroupResource())

	assert.True(t, IsDefaultEncryptionTransformer(serviceAccountTransformer))
	assert.False(t, IsDefaultEncryptionTransformer(deploymentTransformer))
}

func TestIsDefaultEncryptionTransformer_SpecificResource(t *testing.T) {
	encryptionConfigFilepath := filepath.Join("testdata", "encryption-provider-config-specific-resource.yaml")
	transformers, err := PrepareEncryptionTransformersFromConfig(context.Background(), encryptionConfigFilepath)
	assert.Nil(t, err)

	serviceAccountTransformer := transformers.TransformerForResource(serviceAccountGVR.GroupResource())
	deploymentTransformer := transformers.TransformerForResource(deploymentGVR.GroupResource())

	assert.False(t, IsDefaultEncryptionTransformer(serviceAccountTransformer))
	assert.True(t, IsDefaultEncryptionTransformer(deploymentTransformer))
}
