package objectstore

import (
	"testing"

	"github.com/minio/minio-go/v7"
	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
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

func Test_getBucketLookupType(t *testing.T) {
	tests := []struct {
		name     string
		bc       *v1.S3ObjectStore
		expected minio.BucketLookupType
	}{
		{
			name:     "default lookup when S3ObjectStore is nil",
			bc:       nil,
			expected: minio.BucketLookupAuto,
		},
		{
			name: "default lookup when config is nil and endpoint is AWS",
			bc: &v1.S3ObjectStore{
				Endpoint: "s3.amazonaws.com",
			},
			expected: minio.BucketLookupAuto,
		},
		{
			name: "default lookup when config is nil and endpoint is empty",
			bc: &v1.S3ObjectStore{
				Endpoint: "",
			},
			expected: minio.BucketLookupAuto,
		},
		{
			name: "aliyun endpoint defaults to DNS",
			bc: &v1.S3ObjectStore{
				Endpoint: "oss-cn-hangzhou.aliyuncs.com",
			},
			expected: minio.BucketLookupDNS,
		},
		{
			name: "force lookup type to dns via clientConfig",
			bc: &v1.S3ObjectStore{
				Endpoint: "obs.eu-west-101.myhuaweicloud.eu",
				ClientConfig: &v1.ClientConfig{
					BucketLookup: "dns",
				},
			},
			expected: minio.BucketLookupDNS,
		},
		{
			name: "force lookup type to path via clientConfig",
			bc: &v1.S3ObjectStore{
				Endpoint: "s3.amazonaws.com",
				ClientConfig: &v1.ClientConfig{
					BucketLookup: "path",
				},
			},
			expected: minio.BucketLookupPath,
		},
		{
			name: "force lookup type to auto via clientConfig",
			bc: &v1.S3ObjectStore{
				Endpoint: "oss-cn-hangzhou.aliyuncs.com",
				ClientConfig: &v1.ClientConfig{
					BucketLookup: "auto",
				},
			},
			expected: minio.BucketLookupAuto,
		},
		{
			name: "invalid lookup type in clientConfig falls back to endpoint default",
			bc: &v1.S3ObjectStore{
				Endpoint: "oss-cn-hangzhou.aliyuncs.com",
				ClientConfig: &v1.ClientConfig{
					BucketLookup: "invalid",
				},
			},
			expected: minio.BucketLookupDNS,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := getBucketLookupType(test.bc)
			assert.Equal(t, test.expected, result)
		})
	}
}
