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
	BackupFileName            string `json:"backupFileName"`
	BackupStorageLocation     `json:"backupStorageLocation"`
	BackupTemplate            string `json:"backupTemplate"`
	EncryptionConfigName      string `json:"encryptionConfigName"`
	EncryptionConfigNamespace string `json:"encryptionConfigNamespace"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type BackupTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	BackupFilters []BackupFilter `json:"backupFilters"`
}

type BackupFilter struct {
	ApiGroup          string   `json:"apiGroup"`
	Kinds             []string `json:"kinds"`
	KindsRegex        string   `json:"kindsRegex"`
	ResourceNames     []string `json:"resourceNames"`
	ResourceNameRegex string   `json:"resourceNameRegex"`
	Namespaces        []string `json:"namespaces"`
	NamespaceRegex    string   `json:"namespaceRegex"`
	LabelSelectors    string   `json:"labelSelectors"`
	Prune             bool     `json:"prune"`
}

var (
	BackupConditionReady    string
	BackupConditionUploaded string
)

type BackupStatus struct {
	Conditions []genericcondition.GenericCondition `json:"conditions,omitempty"`
	Summary    string                              `json:"summary,omitempty"`
}

type BackupStorageLocation struct {
	ObjectStore *ObjectStore `json:"s3objectStore"`
	Local       string       `json:"local"`
}

type ObjectStore struct {
	Endpoint    string `json:"endpoint"`
	EndpointCA  string `json:"endpointCa"`
	Credentials string `json:"credentials"`
	BucketName  string `json:"bucketName"`
	Region      string `json:"region"`
	Folder      string `json:"folder"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type Restore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec RestoreSpec `json:"spec"`
}

type RestoreSpec struct {
	BackupFileName            string `json:"backupFileName"`
	BackupStorageLocation     `json:"backupStorageLocation"`
	PruneRestore              bool   `json:"pruneRestore"`
	ForcePruneTimeout         int    `json:"forcePruneTimeout"`
	EncryptionConfigName      string `json:"encryptionConfigName"`
	EncryptionConfigNamespace string `json:"encryptionConfigNamespace"`
}
