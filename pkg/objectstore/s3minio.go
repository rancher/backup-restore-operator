package objectstore

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"github.com/rancher/backup-restore-operator/cmd/operator/version"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/s3utils"
	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/backup-restore-operator/pkg/util"
	log "github.com/sirupsen/logrus"
)

type ObjectStore struct {
	Endpoint                  string `json:"endpoint"`
	EndpointCA                string `json:"endpointCA"`
	InsecureTLSSkipVerify     string `json:"insecureTLSSkipVerify"`
	CredentialSecretName      string `json:"credentialSecretName"`
	CredentialSecretNamespace string `json:"credentialSecretNamespace"`
	BucketName                string `json:"bucketName"`
	Region                    string `json:"region"`
	Folder                    string `json:"folder"`
}

// Almost everything in this file is from rke-tools with some modifications https://github.com/rancher/rke-tools/blob/master/main.go

const (
	s3ServerRetries = 3
	s3Endpoint      = "s3.amazonaws.com"
	contentType     = "application/gzip"
)

func SetS3Service(bc *v1.S3ObjectStore, accessKey, secretKey string, useSSL bool) (*minio.Client, error) {
	// Initialize minio client object.
	log.WithFields(log.Fields{
		"s3-endpoint":              bc.Endpoint,
		"s3-bucketName":            bc.BucketName,
		"s3-accessKey":             accessKey,
		"s3-region":                bc.Region,
		"s3-endpoint-ca":           bc.EndpointCA,
		"s3-folder":                bc.Folder,
		"insecure-tls-skip-verify": bc.InsecureTLSSkipVerify,
	}).Info("invoking set s3 service client")

	var err error
	var client = &minio.Client{}
	var cred credentials.Credentials
	var tr = http.DefaultTransport
	bucketLookup := getBucketLookupType(bc.Endpoint)
	for retries := 0; retries <= s3ServerRetries; retries++ {
		// if the s3 access key and secret is not set use iam role
		if len(accessKey) == 0 && len(secretKey) == 0 {
			log.Info("invoking set s3 service client use IAM role")
			// This will work when run on an EC2 instance that has the right policy to access buckets
			cred = *credentials.NewIAM("")
			if bc.Endpoint == "" {
				bc.Endpoint = s3Endpoint
			}
		} else {
			cred = *credentials.NewStatic(accessKey, secretKey, "", credentials.SignatureDefault)
		}
		tr, err = setTransport(tr, bc.EndpointCA, bc.InsecureTLSSkipVerify)
		if err != nil {
			return nil, err
		}
		client, err = minio.New(bc.Endpoint, &minio.Options{
			Creds:        &cred,
			Secure:       useSSL,
			Region:       bc.Region,
			BucketLookup: bucketLookup,
			Transport:    tr,
		})
		if err != nil {
			log.Infof("failed to init s3 client server: %v, retried %d times", err, retries)
			if retries >= s3ServerRetries {
				return nil, fmt.Errorf("failed to set s3 server: %v", err)
			}
			continue
		}

		break
	}
	client.SetAppInfo("rancher backup-restore-operator", version.Version)

	// TODO: check bucket exists after returning basic configured client
	// this way any config's that could affect the out come of checking will be setup first.
	found, err := client.BucketExists(context.Background(), bc.BucketName)
	if err != nil {
		return nil, fmt.Errorf("failed to check if s3 bucket [%s] exists, error: %v", bc.BucketName, err)
	}
	if !found {
		return nil, fmt.Errorf("s3 bucket [%s] not found", bc.BucketName)
	}
	return client, nil
}

// GetS3Client prepares the S3 client per the current BRO config requirements
// TODO: namespace should be backup.NS only if backup CR contains storage location, for using operator's s3, use chart's ns
func GetS3Client(ctx context.Context, objectStore *v1.S3ObjectStore, dynamicClient dynamic.Interface, clientConfig *v1.ClientConfig) (*minio.Client, error) {
	var accessKey, secretKey string
	var notFoundKeys []string
	if objectStore.CredentialSecretName != "" {
		gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
		secrets := dynamicClient.Resource(gvr)
		secretNs, secretName := objectStore.CredentialSecretNamespace, objectStore.CredentialSecretName
		s3secret, err := secrets.Namespace(secretNs).Get(ctx, secretName, k8sv1.GetOptions{})
		if err != nil {
			return &minio.Client{}, err
		}
		s3SecretData, ok := s3secret.Object["data"].(map[string]interface{})
		if !ok {
			return &minio.Client{}, fmt.Errorf("malformed secret [%s] in namespace [%s], unable to read the data field", secretName, secretNs)
		}
		accessKeyEncoded, foundAccessKey := s3SecretData["accessKey"].(string)
		if !foundAccessKey {
			notFoundKeys = append(notFoundKeys, "accessKey")
		}
		secretKeyEncoded, foundSecretKey := s3SecretData["secretKey"].(string)
		if !foundSecretKey {
			notFoundKeys = append(notFoundKeys, "secretKey")
		}
		if len(notFoundKeys) > 0 {
			return &minio.Client{}, fmt.Errorf("malformed secret [%s] in namespace [%s], the following keys were not found in the data field: [%s]", secretName, secretNs, strings.Join(notFoundKeys, ","))
		}
		accessKeyBytes, err := base64.StdEncoding.DecodeString(accessKeyEncoded)
		if err != nil {
			return &minio.Client{}, fmt.Errorf("malformed secret [%s] in namespace [%s], accessKey could not be base64 decoded: %v", secretName, secretNs, err)
		}
		accessKey = string(accessKeyBytes)
		log.Debugf("Found accessKey [%s] in secret [%s] in namespace [%s]", accessKey, secretName, secretNs)
		secretKeyBytes, err := base64.StdEncoding.DecodeString(secretKeyEncoded)
		if err != nil {
			return &minio.Client{}, fmt.Errorf("malformed secret [%s] in namespace [%s], secretKey could not be base64 decoded: %v", secretName, secretNs, err)
		}
		secretKey = string(secretKeyBytes)
		log.Tracef("Found secretKey [%s] in secret [%s] in namespace [%s]", secretKey, secretName, secretNs)
	}
	ctxca, caT := context.WithTimeout(ctx, 5*time.Millisecond)
	defer caT()
	devMode := util.DevModeContext(ctxca)
	// if no s3 credentials are provided, use IAM profile, this means passing empty access and secret keys to the SetS3Service call
	s3Client, err := SetS3Service(objectStore, accessKey, secretKey, !devMode)
	if err != nil {
		return &minio.Client{}, err
	}

	// Because we only have an AWS specific config for now check both at the same time
	// if/when we add other configs we can refactor this a bit.
	if clientConfig != nil && clientConfig.Aws != nil {
		// When the client config AWS dual-stack setting is set to false we disable it
		// This is enabled by default and in some user environments this has caused issues
		if clientConfig.Aws.DualStack == false {
			log.Debug("disabling the AWS S3 dual-stack client setting")
			s3Client.SetS3EnableDualstack(false)
		}
	}

	return s3Client, nil
}

func getBucketLookupType(endpoint string) minio.BucketLookupType {
	if endpoint == "" {
		return minio.BucketLookupAuto
	}
	if strings.Contains(endpoint, "aliyun") {
		return minio.BucketLookupDNS
	}
	return minio.BucketLookupAuto
}

func UploadBackupFile(svc *minio.Client, bucketName, fileName, filePath string) error {
	// Upload the zip file with FPutObject
	log.Infof("invoking uploading backup file [%s] to s3", fileName)
	for retries := 0; retries <= s3ServerRetries; retries++ {
		uploadInfo, err := svc.FPutObject(context.Background(), bucketName, fileName, filePath, minio.PutObjectOptions{ContentType: contentType})
		if err != nil {
			log.Infof("failed to upload backup file [%s], error: %v, retried %d times", fileName, err, retries)
			if retries >= s3ServerRetries {
				return fmt.Errorf("failed to upload backup file [%s], error: %v", fileName, err)
			}
			continue
		}
		log.Debugf("uploadInfo for [%s] is: %v", fileName, uploadInfo)
		log.Infof("Successfully uploaded [%s]", fileName)
		break
	}
	return nil
}

func DownloadFromS3WithPrefix(client *minio.Client, prefix, bucket string) (string, error) {
	var filename string
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var objectCh <-chan minio.ObjectInfo
	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: false,
	}
	if s3utils.IsGoogleEndpoint(*client.EndpointURL()) {
		log.Info("Endpoint is Google GCS")
		opts.UseV1 = true
	}
	objectCh = client.ListObjects(ctx, bucket, opts)

	for object := range objectCh {
		if object.Err != nil {
			log.Errorf("failed to list objects in backup buckets [%s]: %v", bucket, object.Err)
			return "", object.Err
		}

		if prefix == object.Key {
			filename = object.Key
			break
		}
	}
	if len(filename) == 0 {
		return "", fmt.Errorf("failed to download s3 backup: no backup files found with prefix [%s] in bucket [%s]", prefix, bucket)
	}
	// if folder is included, strip it so it doesnt end up in a folder on the host itself
	targetFilename := path.Base(filename)
	targetFileLocation := filepath.Join(os.TempDir(), targetFilename)
	log.Infof("Temporary location of backup file from s3: %v", targetFileLocation)
	var object *minio.Object
	var err error
	for retries := 0; retries <= s3ServerRetries; retries++ {
		object, err = client.GetObject(context.Background(), bucket, filename, minio.GetObjectOptions{})
		if err != nil {
			log.Infof("Failed to download backup file [%s] from bucket [%s]: %v, retried %d times", filename, bucket, err, retries)
			if retries >= s3ServerRetries {
				return "", fmt.Errorf("unable to download backup file [%s] from bucket [%s]: %v", filename, bucket, err)
			}
		}
		if err == nil {
			log.Infof("Successfully downloaded backup file [%s] from bucket [%s]", filename, bucket)
			break
		}
	}

	localFile, err := os.Create(targetFileLocation)
	if err != nil {
		return "", fmt.Errorf("failed to create local file [%s]: %v", targetFileLocation, err)
	}
	defer localFile.Close()

	if _, err = io.Copy(localFile, object); err != nil {
		return "", fmt.Errorf("failed to copy retrieved object to local file [%s]: %v", targetFileLocation, err)
	}
	if err := os.Chmod(targetFileLocation, 0600); err != nil {
		return "", fmt.Errorf("changing permission of the locally downloaded snapshot failed")
	}

	return targetFileLocation, nil
}

func setTransport(tr http.RoundTripper, endpointCA string, insecureSkipVerify bool) (http.RoundTripper, error) {
	certPool := x509.NewCertPool()
	tlsConfig := &tls.Config{}
	if endpointCA != "" {
		ca, err := readS3EndpointCA(endpointCA)
		if err != nil {
			return tr, err
		}
		if !isValidCertificate(ca) {
			return tr, fmt.Errorf("s3-endpoint-ca is not a valid x509 certificate")
		}
		certPool.AppendCertsFromPEM(ca)
		tlsConfig.RootCAs = certPool
	}
	tlsConfig.InsecureSkipVerify = insecureSkipVerify
	tr.(*http.Transport).TLSClientConfig = tlsConfig

	return tr, nil
}

func readS3EndpointCA(endpointCA string) ([]byte, error) {
	// I expect the CA to be passed as base64 string OR a file system path.
	// I do this to be able to pass it through rke/rancher api without writing it
	// to the backup container filesystem.
	ca, err := base64.StdEncoding.DecodeString(endpointCA)
	if err == nil {
		log.Info("reading s3-endpoint-ca as a base64 string")
	} else {
		ca, err = os.ReadFile(endpointCA)
		log.Infof("reading s3-endpoint-ca from [%v]", endpointCA)
	}
	return ca, err
}

func isValidCertificate(c []byte) bool {
	p, _ := pem.Decode(c)
	if p == nil {
		return false
	}
	_, err := x509.ParseCertificates(p.Bytes)
	return err == nil
}
