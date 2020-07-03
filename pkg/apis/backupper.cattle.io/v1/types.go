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
	BackupStorageLocation           `json:"backupStorageLocation"`
	GroupVersions                   string         `json:"groupVersions"`
	BackupFilters                   []BackupFilter `json:"resources"`
	BackupEncryptionSecretName      string         `json:"backupEncryptionSecretName"`
	BackupEncryptionSecretAlgorithm string         `json:"backupEncryptionSecretAlgorithm"`
}

type BackupFilter struct {
	ApiGroup          string   `json:"apiGroup"`
	Kinds             string   `json:"kinds"`
	ResourceNames     []string `json:"resourceNames"`
	ResourceNameRegex string   `json:"resourceNameRegex"`
	Namespaces        []string `json:"namespaces"`
	NamespaceRegex    string   `json:"namespaceRegex"`
}

type BackupStatus struct {
	Conditions []genericcondition.GenericCondition `json:"conditions,omitempty"`
	Summary    string                              `json:"summary,omitempty"`
}

type BackupStorageLocation struct {
	ObjectStore string `json:"objectStore"`
	Local       string `json:"local"`
}

type BackupObjectStore struct {
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Restore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec RestoreSpec `json:"spec"`
}

type RestoreSpec struct {
	BackupName string `json:"backupName"`
}
