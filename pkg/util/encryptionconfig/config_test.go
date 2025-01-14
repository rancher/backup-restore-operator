package encryptionconfig

import (
	"context"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

func TestPrepareEncryptionTransformersFromConfig_Wildcard(t *testing.T) {
	encryptionConfigFilepath := filepath.Join("testdata", "encryption-provider-config-wildcard.yaml")
	transformers, err := PrepareEncryptionTransformersFromConfig(context.Background(), encryptionConfigFilepath)
	if err != nil {
		return
	}

	assert.NotNil(t, transformers.TransformerForResource(serviceAccountGVR.GroupResource()))
	assert.NotNil(t, transformers.TransformerForResource(deploymentGVR.GroupResource()))
	assert.NotEqual(t, transformers.TransformerForResource(deploymentGVR.GroupResource()), identity.NewEncryptCheckTransformer())
}

func TestPrepareEncryptionTransformersFromConfig_ErrorsWithInvalidConfig(t *testing.T) {
	encryptionConfigFilepath := filepath.Join("testdata", "encryption-provider-config-invalid.yaml")
	_, err := PrepareEncryptionTransformersFromConfig(context.Background(), encryptionConfigFilepath)
	if err == nil {
		return
	}

	assert.Error(t, err)
	assert.ErrorContains(t, err, "error while parsing file: error decoding encryption provider configuration file")
}

func TestIsDefaultEncryptionTransformer_Wildcard(t *testing.T) {
	encryptionConfigFilepath := filepath.Join("testdata", "encryption-provider-config-wildcard.yaml")
	transformers, err := PrepareEncryptionTransformersFromConfig(context.Background(), encryptionConfigFilepath)
	if err != nil {
		return
	}

	serviceAccountTransformer := transformers.TransformerForResource(serviceAccountGVR.GroupResource())
	deploymentTransformer := transformers.TransformerForResource(deploymentGVR.GroupResource())

	assert.False(t, IsDefaultEncryptionTransformer(serviceAccountTransformer))
	assert.False(t, IsDefaultEncryptionTransformer(deploymentTransformer))
}

func TestIsDefaultEncryptionTransformer_PartialWildcard(t *testing.T) {
	encryptionConfigFilepath := filepath.Join("testdata", "encryption-provider-config-partial-wildcard.yaml")
	transformers, err := PrepareEncryptionTransformersFromConfig(context.Background(), encryptionConfigFilepath)
	if err != nil {
		return
	}

	serviceAccountTransformer := transformers.TransformerForResource(serviceAccountGVR.GroupResource())
	deploymentTransformer := transformers.TransformerForResource(deploymentGVR.GroupResource())

	assert.True(t, IsDefaultEncryptionTransformer(serviceAccountTransformer))
	assert.False(t, IsDefaultEncryptionTransformer(deploymentTransformer))
}

func TestIsDefaultEncryptionTransformer_SpecificResource(t *testing.T) {
	encryptionConfigFilepath := filepath.Join("testdata", "encryption-provider-config-specific-resource.yaml")
	transformers, err := PrepareEncryptionTransformersFromConfig(context.Background(), encryptionConfigFilepath)
	if err != nil {
		return
	}

	serviceAccountTransformer := transformers.TransformerForResource(serviceAccountGVR.GroupResource())
	deploymentTransformer := transformers.TransformerForResource(deploymentGVR.GroupResource())

	assert.False(t, IsDefaultEncryptionTransformer(serviceAccountTransformer))
	assert.True(t, IsDefaultEncryptionTransformer(deploymentTransformer))
}
