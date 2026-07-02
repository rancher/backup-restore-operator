package v1

import (
	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
)

// BackupType enforces the valid registration modes
// +kubebuilder:validation:Enum=One-time;Recurring
type BackupType string

const (
	OneTimeBackupType   BackupType = "One-time"
	RecurringBackupType BackupType = "Recurring"
)

var (
	BackupConditionReady        condition.Cond = "Ready"
	BackupConditionUploaded     condition.Cond = "Uploaded"
	BackupConditionReconciling  condition.Cond = "Reconciling"
	BackupConditionStalled      condition.Cond = "Stalled"
	RestoreConditionReconciling condition.Cond = "Reconciling"
	RestoreConditionStalled     condition.Cond = "Stalled"
	RestoreConditionReady       condition.Cond = "Ready"
)

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Location",type=string,JSONPath=`.status.storageLocation`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.status.backupType`
// +kubebuilder:printcolumn:name="Latest-Backup",type=string,JSONPath=`.status.filename`
// +kubebuilder:printcolumn:name="ResourceSet",type=string,JSONPath=`.spec.resourceSetName`
// +kubebuilder:printcolumn:name="Age",type=date-time,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].message`
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Backup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BackupSpec   `json:"spec"`
	Status BackupStatus `json:"status"`
}

type BackupSpec struct {
	// +nullable
	StorageLocation *StorageLocation `json:"storageLocation"`
	// Name of the ResourceSet CR to use for backup
	// +required
	ResourceSetName string `json:"resourceSetName"`
	// Name of the Secret containing the encryption config
	// +kubebuilder:validation:nullable
	// +nullable
	EncryptionConfigSecretName string `json:"encryptionConfigSecretName,omitempty"`
	// Cron schedule for recurring backups
	// +kubebuilder:example="Descriptors: '@midnight'\nStandard crontab specs: 0 0 * * *"
	// +kubebuilder:validation:nullable
	// +nullable
	Schedule string `json:"schedule,omitempty"`
	// +kubebuilder:validation:Minimum=1
	RetentionCount int64 `json:"retentionCount,omitempty"`
}

type BackupStatus struct {
	Conditions         []genericcondition.GenericCondition `json:"conditions"`
	LastSnapshotTS     string                              `json:"lastSnapshotTs"`
	NextSnapshotAt     string                              `json:"nextSnapshotAt"`
	ObservedGeneration int64                               `json:"observedGeneration"`
	StorageLocation    string                              `json:"storageLocation"`
	BackupType         BackupType                          `json:"backupType"`
	Filename           string                              `json:"filename"`
	Summary            string                              `json:"summary"`
}

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ResourceSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	ResourceSelectors    []ResourceSelector    `json:"resourceSelectors"`
	ControllerReferences []ControllerReference `json:"controllerReferences"`
}

// regex+list = OR //separate fields :AND
type ResourceSelector struct {
	// +required
	APIVersion                string                `json:"apiVersion"`
	Kinds                     []string              `json:"kinds,omitempty"`
	KindsRegexp               string                `json:"kindsRegexp,omitempty"`
	ResourceNames             []string              `json:"resourceNames,omitempty"`
	ResourceNameRegexp        string                `json:"resourceNameRegexp,omitempty"`
	Namespaces                []string              `json:"namespaces,omitempty"`
	NamespaceRegexp           string                `json:"namespaceRegexp,omitempty"`
	LabelSelectors            *metav1.LabelSelector `json:"labelSelectors,omitempty"`
	FieldSelectors            fields.Set            `json:"fieldSelectors,omitempty"`
	ExcludeKinds              []string              `json:"excludeKinds,omitempty"`
	ExcludeResourceNameRegexp string                `json:"excludeResourceNameRegexp,omitempty"`
}

type ControllerReference struct {
	APIVersion string `json:"apiVersion"`
	Resource   string `json:"resource"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	Replicas   int32  `json:"replicas"`
}

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

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Backup-Source",type=string,JSONPath=`.status.backupSource`
// +kubebuilder:printcolumn:name="Backup-File",type=string,JSONPath=`.spec.backupFilename`
// +kubebuilder:printcolumn:name="Age",type=date-time,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].message`
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Restore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RestoreSpec   `json:"spec"`
	Status RestoreStatus `json:"status"`
}

type RestoreSpec struct {
	// +required
	BackupFilename string `json:"backupFilename"`
	// +nullable
	StorageLocation *StorageLocation `json:"storageLocation"`
	// prune is true by default when unset
	// +kubebuilder:default:=true
	// +optional
	Prune *bool `json:"prune"`
	// +kubebuilder:validation:Maximum=10
	DeleteTimeoutSeconds       int    `json:"deleteTimeoutSeconds,omitempty"`
	EncryptionConfigSecretName string `json:"encryptionConfigSecretName,omitempty"`

	// When set to true, the controller ignores any errors during the restore process
	IgnoreErrors bool `json:"ignoreErrors,omitempty"`
}

type RestoreStatus struct {
	Conditions          []genericcondition.GenericCondition `json:"conditions,omitempty"`
	RestoreCompletionTS string                              `json:"restoreCompletionTs"`
	ObservedGeneration  int64                               `json:"observedGeneration"`
	BackupSource        string                              `json:"backupSource"`
	Summary             string                              `json:"summary"`
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
