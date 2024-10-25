package util

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/server/options/encryptionconfig"
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

	var staticTransformers encryptionconfig.StaticTransformers = transformers

	assert.NotNil(t, staticTransformers.TransformerForResource(serviceAccountGVR.GroupResource()))
	assert.NotNil(t, staticTransformers.TransformerForResource(deploymentGVR.GroupResource()))
	assert.NotEqual(t, staticTransformers.TransformerForResource(deploymentGVR.GroupResource()), identity.NewEncryptCheckTransformer())
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

func TestErrList_VerifyErrorConcatenation(t *testing.T) {
	errList := []error{
		fmt.Errorf("error1"),
		fmt.Errorf("error2"),
		fmt.Errorf("error3"),
		fmt.Errorf("error4"),
		fmt.Errorf("error5"),
	}

	mergedErrors := ErrList(errList)
	assert.ErrorContains(t, mergedErrors, "error1")
	assert.ErrorContains(t, mergedErrors, "error5")

	errList = []error{
		fmt.Errorf("error1"),
	}

	mergedErrors = ErrList(errList)
	assert.ErrorContains(t, mergedErrors, "error1")
}

func TestErrList_VerifyNilOnEmptyList(t *testing.T) {
	errList := []error{}

	mergedErrors := ErrList(errList)
	assert.Nil(t, mergedErrors)
}
