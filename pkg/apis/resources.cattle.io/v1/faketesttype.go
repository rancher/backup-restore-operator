package v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type FakeTest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FakeSpec   `json:"spec"`
	Status FakeStatus `json:"status"`
}

type FakeSpec struct {
	ValStr string `json:"valStr"`
	ValInt int    `json:"valInt"`
}

type FakeStatus struct {
	Generation int    `json:"generation"`
	Version    string `json:"version"`
}
