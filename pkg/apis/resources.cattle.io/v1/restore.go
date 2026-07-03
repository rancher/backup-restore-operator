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
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].message`
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Restore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RestoreSpec   `json:"spec"`
	Status RestoreStatus `json:"status,omitempty"`
}

type RestoreSpec struct {
	// +required
	BackupFilename string `json:"backupFilename"`
	// +optional
	// +nullable
	StorageLocation *StorageLocation `json:"storageLocation,omitempty"`
	// prune is true by default when unset
	// +kubebuilder:default:=true
	// +optional
	// +nullable
	Prune *bool `json:"prune,omitempty"`
	// +kubebuilder:validation:Maximum=10
	// +optional
	DeleteTimeoutSeconds int `json:"deleteTimeoutSeconds,omitempty"`
	// +optional
	EncryptionConfigSecretName string `json:"encryptionConfigSecretName,omitempty"`

	// When set to true, the controller ignores any errors during the restore process
	// +optional
	IgnoreErrors bool `json:"ignoreErrors,omitempty"`
}

// GetPrune returns the prune value, defaulting to true if unset
// This helper consolidates the existing logic of Prune value in a single place.
func (rs *RestoreSpec) GetPrune() bool {
	if rs.Prune == nil {
		return true
	}
	return *rs.Prune
}

type RestoreStatus struct {
	Conditions          []genericcondition.GenericCondition `json:"conditions,omitempty"`
	RestoreCompletionTS string                              `json:"restoreCompletionTs,omitempty"`
	ObservedGeneration  int64                               `json:"observedGeneration,omitempty"`
	BackupSource        string                              `json:"backupSource,omitempty"`
	Summary             string                              `json:"summary,omitempty"`
}
