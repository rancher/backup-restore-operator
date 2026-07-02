package v1

type StorageLocation struct {
	S3 *S3ObjectStore `json:"s3"`
}

type S3ObjectStore struct {
	Endpoint                  string        `json:"endpoint"`
	EndpointCA                string        `json:"endpointCA"`
	InsecureTLSSkipVerify     bool          `json:"insecureTLSSkipVerify"`
	CredentialSecretName      string        `json:"credentialSecretName"`
	CredentialSecretNamespace string        `json:"credentialSecretNamespace"`
	BucketName                string        `json:"bucketName"`
	Region                    string        `json:"region"`
	Folder                    string        `json:"folder"`
	ClientConfig              *ClientConfig `json:"clientConfig,omitempty"`
}

// ClientConfig allows configuration of more advanced minio client settings
// any provider specific settings will be grouped accordingly, otherwise settings apply to all S3 providres.
type ClientConfig struct {
	// TODO: Add setting to control lookup mode
	// TODO: Add a setting for varying trace options that minio provides
	Aws *AwsConfig `json:"aws,omitempty"`
}

type AwsConfig struct {
	// +default:value=true
	DualStack bool `json:"dualStack"`
	// TODO: also support s3 TransferAccelerate feature this way?
}
