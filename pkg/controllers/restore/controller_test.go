package restore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestCrdKind(t *testing.T) {
	mockKind := "Backup"
	mockCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"names": map[string]interface{}{
					"kind": mockKind,
				},
			},
		},
	}
	kind := crdKind(mockCRD)
	assert.Equal(t, mockKind, kind, "CRD Kind does not match expected value")
}
