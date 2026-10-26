package objectstore

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// defaultTransportTLS returns the TLS configuration of the shared default
// transport. The standard library fills in an empty one the first time the
// transport is cloned or used, so only its contents matter.
func defaultTransportTLS() *tls.Config {
	return http.DefaultTransport.(*http.Transport).TLSClientConfig
}

func Test_setTransport_leavesDefaultTransportAlone(t *testing.T) {
	insecure, err := setTransport("", true)
	require.NoError(t, err)
	assert.True(t, insecure.TLSClientConfig.InsecureSkipVerify)
	if c := defaultTransportTLS(); c != nil {
		assert.False(t, c.InsecureSkipVerify, "the default transport must keep verifying certificates")
	}

	verified, err := setTransport("", false)
	require.NoError(t, err)
	assert.False(t, verified.TLSClientConfig.InsecureSkipVerify)
	assert.True(t, insecure.TLSClientConfig.InsecureSkipVerify, "a transport keeps its setting when another store's transport is created")
}

func Test_setTransport_endpointCAOnlyOnItsTransport(t *testing.T) {
	tr, err := setTransport(base64.StdEncoding.EncodeToString(selfSignedCAPEM(t)), false)
	require.NoError(t, err)
	require.NotNil(t, tr.TLSClientConfig.RootCAs)
	assert.False(t, tr.TLSClientConfig.InsecureSkipVerify)
	if c := defaultTransportTLS(); c != nil {
		assert.Nil(t, c.RootCAs, "the default transport must not be limited to the store's CA")
	}

	other, err := setTransport("", false)
	require.NoError(t, err)
	assert.Nil(t, other.TLSClientConfig.RootCAs, "another store must not inherit the CA")
}

func selfSignedCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
