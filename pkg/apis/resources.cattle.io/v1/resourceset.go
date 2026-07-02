package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
)

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
