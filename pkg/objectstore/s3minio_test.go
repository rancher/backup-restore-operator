package objectstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_redactAccessKeyID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "common AWS access key for IAM user",
			input:    "AKIAIOSFODNN7EXAMPLE",
			expected: "AKIAI*********",
		},
		{
			name:     "common AWS access key for temp Security credentials",
			input:    "ASIAJEXAMPLEXEG2JICEA",
			expected: "ASIAJ*********",
		},
		{
			name:     "very short access key",
			input:    "ab",
			expected: redactStringMask,
		},
		{
			name:     "short access key",
			input:    "abcdef",
			expected: redactStringMask,
		},
		{
			name:     "normal access key",
			input:    "abcdefghijklmnopqrst",
			expected: "abcde*********",
		},
		{
			name:     "super long access key",
			input:    "ADOGjumpsOverTheSleepyFrogLayingOnTheLOG",
			expected: "ADOGj*********",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := redactAccessKeyID(test.input)
			assert.Equal(t, test.expected, result)
		})
	}
}
