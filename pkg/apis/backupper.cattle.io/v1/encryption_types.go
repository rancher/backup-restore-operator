package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// This file contains all encyrptionConfig types from apiserver https://github.com/kubernetes/kubernetes/blob/master/staging/src/k8s.io/apiserver/pkg/apis/config/types.go

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type BackupEncryptionConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// resources is a list containing resources, and their corresponding encryption providers.
	Resources []ResourceConfiguration `json:"resources"`
}

// ResourceConfiguration stores per resource configuration.
type ResourceConfiguration struct {
	// resources is a list of kubernetes resources which have to be encrypted.
	Resources []string `json:"resources"`
	// providers is a list of transformers to be used for reading and writing the resources to disk.
	// eg: aesgcm, aescbc, secretbox, identity.
	Providers []ProviderConfiguration `json:"providers"`
}

// ProviderConfiguration stores the provided configuration for an encryption provider.
type ProviderConfiguration struct {
	// aesgcm is the configuration for the AES-GCM transformer.
	AESGCM *AESConfiguration `json:"aesgcm"`
	// aescbc is the configuration for the AES-CBC transformer.
	AESCBC *AESConfiguration `json:"aescbc"`
	// secretbox is the configuration for the Secretbox based transformer.
	Secretbox *SecretboxConfiguration `json:"secretbox"`
	// identity is the (empty) configuration for the identity transformer.
	Identity *IdentityConfiguration `json:"identity"`
	// kms contains the name, cache size and path to configuration file for a KMS based envelope transformer.
	KMS *KMSConfiguration `json:"kms"`
}

// AESConfiguration contains the API configuration for an AES transformer.
type AESConfiguration struct {
	// keys is a list of keys to be used for creating the AES transformer.
	// Each key has to be 32 bytes long for AES-CBC and 16, 24 or 32 bytes for AES-GCM.
	Keys []Key `json:"keys"`
}

// SecretboxConfiguration contains the API configuration for an Secretbox transformer.
type SecretboxConfiguration struct {
	// keys is a list of keys to be used for creating the Secretbox transformer.
	// Each key has to be 32 bytes long.
	Keys []Key `json:"keys"`
}

// Key contains name and secret of the provided key for a transformer.
type Key struct {
	// name is the name of the key to be used while storing data to disk.
	Name string `json:"name"`
	// secret is the actual key, encoded in base64.
	Secret string `json:"secret"`
}

// IdentityConfiguration is an empty struct to allow identity transformer in provider configuration.
type IdentityConfiguration struct{}

// KMSConfiguration contains the name, cache size and path to configuration file for a KMS based envelope transformer.
type KMSConfiguration struct {
	// name is the name of the KMS plugin to be used.
	Name string `json:"name"`
	// cachesize is the maximum number of secrets which are cached in memory. The default value is 1000.
	// Set to a negative value to disable caching.
	// +optional
	CacheSize *int32 `json:"cacheSize"`
	// endpoint is the gRPC server listening address, for example "unix:///var/run/kms-provider.sock".
	Endpoint string `json:"endpoint"`
	// timeout for gRPC calls to kms-plugin (ex. 5s). The default is 3 seconds.
	// +optional
	Timeout *metav1.Duration `json:"timeout"`
}
