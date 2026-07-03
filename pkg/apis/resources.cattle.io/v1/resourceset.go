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

	// +kubebuilder:default:={}
	// +required
	ResourceSelectors []ResourceSelector `json:"resourceSelectors"`
	// +kubebuilder:default:={}
	// +optional
	ControllerReferences []ControllerReference `json:"controllerReferences,omitempty"`
}

// regex+list = OR //separate fields :AND
type ResourceSelector struct {
	// +required
	APIVersion string `json:"apiVersion"`
	// +optional
	Kinds []string `json:"kinds,omitempty"`
	// +optional
	KindsRegexp string `json:"kindsRegexp,omitempty"`
	// +optional
	ResourceNames []string `json:"resourceNames,omitempty"`
	// +optional
	ResourceNameRegexp string `json:"resourceNameRegexp,omitempty"`
	// +optional
	Namespaces []string `json:"namespaces,omitempty"`
	// +optional
	NamespaceRegexp string `json:"namespaceRegexp,omitempty"`
	// +optional
	// +nullable
	LabelSelectors *metav1.LabelSelector `json:"labelSelectors,omitempty"`
	// +optional
	FieldSelectors fields.Set `json:"fieldSelectors,omitempty"`
	// +optional
	ExcludeKinds []string `json:"excludeKinds,omitempty"`
	// +optional
	ExcludeResourceNameRegexp string `json:"excludeResourceNameRegexp,omitempty"`
}

type ControllerReference struct {
	APIVersion string `json:"apiVersion"`
	Resource   string `json:"resource"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	Replicas   int32  `json:"replicas"`
}
