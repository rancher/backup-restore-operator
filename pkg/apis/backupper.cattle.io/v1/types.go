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
	BackupStorageLocation  `json:"backupStorageLocation"`
	BackupTemplate         string                 `json:"backupTemplate"`
	BackupEncryptionConfig BackupEncryptionConfig `json:"backupEncryptionConfig"`
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
	Prune             string   `json:"prune"`
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
	BackupName            string `json:"backupName"`
	BackupStorageLocation `json:"backupStorageLocation"`
	PruneRestore          bool `json:"pruneRestore"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type BackupEncryptionConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	EncryptionProvider string `json:"encryptionProvider"`
	EncryptionSecret   string `json:"encryptionSecret"` // refer a secret
	KMSConfiguration   `json:"kmsconfig"`
}

// Refers https://github.com/kubernetes/kubernetes/blob/master/staging/src/k8s.io/apiserver/pkg/apis/config/v1/types.go#L87
type KMSConfiguration struct {
	// name is the name of the KMS plugin to be used.
	PluginName string `json:"pluginName"`
	// cachesize is the maximum number of secrets which are cached in memory. The default value is 1000.
	// Set to a negative value to disable caching.
	// +optional
	CacheSize *int32 `json:"cacheSize"`
	// endpoint is the gRPC server listening address, for example "unix:///var/run/kms-provider.sock".
	Endpoint string `json:"endpoint"`
	// timeout for gRPC calls to kms-plugin (ex. 5s). The default is 3 seconds.
	// +optional
	Timeout *metav1.Duration `json:"timeout"`
}
