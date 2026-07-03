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
	InsecureTLSSkipVerify bool   `json:"insecureTLSSkipVerify,omitempty"`
	CredentialSecretName  string `json:"credentialSecretName"`
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
// any provider specific settings will be grouped accordingly, otherwise settings apply to all S3 providres.
type ClientConfig struct {
	// TODO: Add setting to control lookup mode
	// TODO: Add a setting for varying trace options that minio provides
	// +optional
	// +nullable
	Aws *AwsConfig `json:"aws,omitempty"`
}

type AwsConfig struct {
	// +default:value=true
	// +optional
	DualStack bool `json:"dualStack,omitempty"`
	// TODO: also support s3 TransferAccelerate feature this way?
}
