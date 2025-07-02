package restore

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func Test_shouldSkipBuiltin(t *testing.T) {
	t.Parallel()
	type args struct {
		resourceTypeName string
		resource         map[string]interface{}
	}
	tests := []struct {
		name     string
		args     args
		expected bool
	}{
		{
			name: "should skip builtin globalrole",
			args: args{
				resourceTypeName: "globalroles",
				resource: map[string]interface{}{
					"builtin": "true",
				},
			},
			expected: true,
		},
		{
			name: "should not skip non-builtin globalrole",
			args: args{
				resourceTypeName: "globalroles",
				resource: map[string]interface{}{
					"builtin": "false",
				},
			},
			expected: false,
		},
		{
			name: "should not skip globalrole without builtin field",
			args: args{
				resourceTypeName: "globalroles",
				resource:         map[string]interface{}{},
			},
			expected: false,
		},
		{
			name: "should skip builtin roletemplate",
			args: args{
				resourceTypeName: "roletemplates",
				resource: map[string]interface{}{
					"builtin": "true",
				},
			},
			expected: true,
		},
		{
			name: "should not skip non-builtin roletemplate",
			args: args{
				resourceTypeName: "roletemplates",
				resource: map[string]interface{}{
					"builtin": "false",
				},
			},
			expected: false,
		},
		{
			name: "should not skip roletemplate without builtin field",
			args: args{
				resourceTypeName: "roletemplates",
				resource:         map[string]interface{}{},
			},
			expected: false,
		},
		{
			name: "should not skip other types with builtin field",
			args: args{
				resourceTypeName: "somethingElse",
				resource: map[string]interface{}{
					"builtin": "true",
				},
			},
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.expected, shouldSkipBuiltin(tt.args.resourceTypeName, tt.args.resource), "shouldSkipBuiltin(%v, %v)", tt.args.resourceTypeName, tt.args.resource)
		})
	}
}
