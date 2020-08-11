package v1

import (
	"github.com/rancher/wrangler/pkg/genericcondition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Backup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BackupSpec   `json:"spec"`
	Status BackupStatus `json:"status"`
}

type BackupSpec struct {
	StorageLocation      *StorageLocation `json:"storageLocation"`
	ResourceSetName      string           `json:"resourceSetName"`
	EncryptionConfigName string           `json:"encryptionConfigName,omitempty"`
	Schedule             string           `json:"schedule,omitempty"`
	Retention            int              `json:"retention,omitempty"` //time to keep snapshot in hours
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ResourceSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	ResourceSelectors []ResourceSelector `json:"resourceSelectors"`
}

// regex+list = OR //separate fields :AND
type ResourceSelector struct {
	ApiGroup           string                `json:"apiGroup"`
	Kinds              []string              `json:"kinds,omitempty"`
	KindsRegexp        string                `json:"kindsRegexp,omitempty"`
	ResourceNames      []string              `json:"resourceNames,omitempty"`
	ResourceNameRegexp string                `json:"resourceNameRegexp,omitempty"`
	Namespaces         []string              `json:"namespaces,omitempty"`
	NamespaceRegexp    string                `json:"namespaceRegexp,omitempty"`
	LabelSelectors     *metav1.LabelSelector `json:"labelSelectors,omitempty"`
}

var (
	BackupConditionReady     = "Ready"
	BackupConditionUploaded  = "Uploaded"
	BackupConditionTriggered = "Triggered"
)

type BackupStatus struct {
	Conditions     []genericcondition.GenericCondition `json:"conditions"`
	LastSnapshotTS string                              `json:"lastSnapshotTs"`
	NextSnapshotAt string                              `json:"nextSnapshotAt"`
	NumSnapshots   int                                 `json:"numSnapshots"`
	Summary        string                              `json:"summary"`
}

type StorageLocation struct {
	S3    *S3ObjectStore `json:"s3objectStore"`
	Local string         `json:"local"`
}

type S3ObjectStore struct {
	Endpoint              string `json:"endpoint"`
	EndpointCA            string `json:"endpointCa"`
	InsecureTLSSkipVerify bool   `json:"insecureTLSSkipVerify"`
	CredentialSecretName  string `json:"credentialSecretName"`
	BucketName            string `json:"bucketName"`
	Region                string `json:"region"`
	Folder                string `json:"folder"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Restore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RestoreSpec   `json:"spec"`
	Status RestoreStatus `json:"status"`
}

type RestoreSpec struct {
	BackupFilename       string           `json:"backupFilename"`
	StorageLocation      *StorageLocation `json:"storageLocation"`
	Prune                *bool            `json:"prune"` //prune by default
	DeleteTimeout        int              `json:"deleteTimeout,omitempty"`
	EncryptionConfigName string           `json:"encryptionConfigName,omitempty"`
}

type RestoreStatus struct {
	Conditions []genericcondition.GenericCondition `json:"conditions,omitempty"`
	Summary    string                              `json:"summary,omitempty"`
}
