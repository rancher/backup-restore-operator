package v1

import (
	"github.com/rancher/wrangler/v3/pkg/condition"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
