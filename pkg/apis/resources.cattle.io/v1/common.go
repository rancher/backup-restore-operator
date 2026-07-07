package v1

type StorageLocation struct {
	// +optional
	// +nullable
	S3 *S3ObjectStore `json:"s3,omitempty"`
}

type S3ObjectStore struct {
	Endpoint string `json:"endpoint"`
	// +optional
	EndpointCA string `json:"endpointCA,omitempty"`
	// +optional
	InsecureTLSSkipVerify bool `json:"insecureTLSSkipVerify,omitempty"`
	// +optional
	CredentialSecretName string `json:"credentialSecretName,omitempty"`
	// +optional
	CredentialSecretNamespace string `json:"credentialSecretNamespace,omitempty"`
	BucketName                string `json:"bucketName"`
	// +optional
	Region string `json:"region,omitempty"`
	// +optional
	Folder string `json:"folder,omitempty"`
	// +optional
	// +nullable
	ClientConfig *ClientConfig `json:"clientConfig,omitempty"`
}

// ClientConfig allows configuration of more advanced minio client settings
// any provider specific settings will be grouped accordingly, otherwise settings apply to all S3 providers.
type ClientConfig struct {
	// TODO: Add setting to control lookup mode
	// TODO: Add a setting for varying trace options that minio provides
	// +optional
	// +nullable
	Aws *AwsConfig `json:"aws,omitempty"`
}

// AwsConfig holds AWS-specific S3 configuration.
type AwsConfig struct {
	// Fields here don't use +optional/omitempty since AwsConfig itself is already optional.
	// This avoids the bool round-trip bug (omitempty strips false → default reapplies → always true)
	// and keeps code simple (no *bool pointers needed).

	// +default:value=true
	DualStack bool `json:"dualStack"`
	// TODO: also support s3 TransferAccelerate feature this way?
}
