package util

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/server/options/encryptionconfig"
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

func TestIsDefaultEncryptionTransformer_Wildcard(t *testing.T) {
	encryptionConfigFilepath := filepath.Join("testdata", "encryption-provider-config-wildcard.yaml")
	transformers, err := PrepareEncryptionTransformersFromConfig(context.Background(), encryptionConfigFilepath)
	if err != nil {
		return
	}

	var staticTransformers encryptionconfig.StaticTransformers = transformers
	serviceAccountTransformer := staticTransformers.TransformerForResource(serviceAccountGVR.GroupResource())
	deploymentTransformer := staticTransformers.TransformerForResource(deploymentGVR.GroupResource())

	assert.False(t, IsDefaultEncryptionTransformer(serviceAccountTransformer))
	assert.False(t, IsDefaultEncryptionTransformer(deploymentTransformer))
}

func TestIsDefaultEncryptionTransformer_PartialWildcard(t *testing.T) {
	encryptionConfigFilepath := filepath.Join("testdata", "encryption-provider-config-partial-wildcard.yaml")
	transformers, err := PrepareEncryptionTransformersFromConfig(context.Background(), encryptionConfigFilepath)
	if err != nil {
		return
	}

	var staticTransformers encryptionconfig.StaticTransformers = transformers
	serviceAccountTransformer := staticTransformers.TransformerForResource(serviceAccountGVR.GroupResource())
	deploymentTransformer := staticTransformers.TransformerForResource(deploymentGVR.GroupResource())

	assert.True(t, IsDefaultEncryptionTransformer(serviceAccountTransformer))
	assert.False(t, IsDefaultEncryptionTransformer(deploymentTransformer))
}

func TestIsDefaultEncryptionTransformer_SpecificResource(t *testing.T) {
	encryptionConfigFilepath := filepath.Join("testdata", "encryption-provider-config-specific-resource.yaml")
	transformers, err := PrepareEncryptionTransformersFromConfig(context.Background(), encryptionConfigFilepath)
	if err != nil {
		return
	}

	var staticTransformers encryptionconfig.StaticTransformers = transformers
	serviceAccountTransformer := staticTransformers.TransformerForResource(serviceAccountGVR.GroupResource())
	deploymentTransformer := staticTransformers.TransformerForResource(deploymentGVR.GroupResource())

	assert.False(t, IsDefaultEncryptionTransformer(serviceAccountTransformer))
	assert.True(t, IsDefaultEncryptionTransformer(deploymentTransformer))
}
