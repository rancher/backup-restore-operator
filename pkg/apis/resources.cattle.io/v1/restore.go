package v1

import (
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
