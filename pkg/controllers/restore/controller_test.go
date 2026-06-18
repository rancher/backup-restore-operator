package restore

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

func TestIsSettingsWebhookError(t *testing.T) {
	settingsGVR := schema.GroupVersionResource{
		Group:    "management.cattle.io",
		Version:  "v3",
		Resource: "settings",
	}
	unrelatedGVR := schema.GroupVersionResource{
		Group:    "apps",
		Version:  "v1",
		Resource: "deployments",
	}

	tests := []struct {
		name     string
		gvr      schema.GroupVersionResource
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			gvr:      settingsGVR,
			err:      nil,
			expected: false,
		},
		{
			name: "read-only setting denied by settings webhook",
			gvr:  settingsGVR,
			err: &apierrors.StatusError{
				ErrStatus: k8sv1.Status{
					Message: "admission webhook \"rancher.cattle.io.settings\" denied the request: setting is read only",
				},
			},
			expected: true,
		},
		{
			name: "env-sourced setting denied by settings webhook",
			gvr:  settingsGVR,
			err: &apierrors.StatusError{
				ErrStatus: k8sv1.Status{
					Message: "admission webhook \"rancher.cattle.io.settings\" denied the request: setting cannot be updated since its value is sourced from an environment variable",
				},
			},
			expected: true,
		},
		{
			name: "error from an unrelated webhook",
			gvr:  settingsGVR,
			err: &apierrors.StatusError{
				ErrStatus: k8sv1.Status{
					Message: "admission webhook \"validation.cattle.io\" denied the request: validation failed",
				},
			},
			expected: false,
		},
		{
			name:     "non-webhook apierror (e.g. NotFound)",
			gvr:      settingsGVR,
			err:      apierrors.NewNotFound(schema.GroupResource{Group: "management.cattle.io", Resource: "settings"}, "cacerts"),
			expected: false,
		},
		{
			name:     "non-webhook generic error",
			gvr:      settingsGVR,
			err:      fmt.Errorf("some generic database connection error"),
			expected: false,
		},
		{
			name: "non-settings GVR with settings-like webhook error message",
			gvr:  unrelatedGVR,
			err: &apierrors.StatusError{
				ErrStatus: k8sv1.Status{
					Message: "admission webhook \"rancher.cattle.io.settings\" denied the request: setting is read only",
				},
			},
			expected: false,
		},
		{
			name: "failed calling webhook error",
			gvr:  settingsGVR,
			err: &apierrors.StatusError{
				ErrStatus: k8sv1.Status{
					Message: "Internal error occurred: failed calling webhook \"rancher.cattle.io.settings\": service \"rancher-webhook\" not found",
				},
			},
			expected: false,
		},
		{
			name: "empty message statuserror falling back to error()",
			gvr:  settingsGVR,
			err: &apierrors.StatusError{
				ErrStatus: k8sv1.Status{
					Message: "",
				},
			},
			expected: false,
		},
		{
			name:     "typed nil apierror pointer",
			gvr:      settingsGVR,
			err:      (*apierrors.StatusError)(nil),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isSettingsWebhookError(tt.gvr, tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
