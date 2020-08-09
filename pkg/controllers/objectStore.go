package controllers

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v6"
	"github.com/minio/minio-go/v6/pkg/credentials"
	v1 "github.com/mrajashree/backup/pkg/apis/resources.cattle.io/v1"
	log "github.com/sirupsen/logrus"
)

const (
	s3ServerRetries = 3
	s3Endpoint      = "s3.amazonaws.com"
	contentType     = "application/gzip"
)

func SetS3Service(bc *v1.S3ObjectStore, accessKey, secretKey string, useSSL bool) (*minio.Client, error) {
	// Initialize minio client object.
	log.WithFields(log.Fields{
		"s3-endpoint":    bc.Endpoint,
		"s3-bucketName":  bc.BucketName,
		"s3-accessKey":   accessKey,
		"s3-region":      bc.Region,
		"s3-endpoint-ca": bc.EndpointCA,
		"s3-folder":      bc.Folder,
	}).Info("invoking set s3 service client")

	var err error
	var client = &minio.Client{}
	var cred = &credentials.Credentials{}
	var tr = http.DefaultTransport
	bucketLookup := getBucketLookupType(bc.Endpoint)
	for retries := 0; retries <= s3ServerRetries; retries++ {
		// if the s3 access key and secret is not set use iam role
		if len(accessKey) == 0 && len(secretKey) == 0 {
			log.Info("invoking set s3 service client use IAM role")
			cred = credentials.NewIAM("")
			if bc.Endpoint == "" {
				bc.Endpoint = s3Endpoint
			}
		} else {
			cred = credentials.NewStatic(accessKey, secretKey, "", credentials.SignatureDefault)
		}
		client, err = minio.NewWithOptions(bc.Endpoint, &minio.Options{
			Creds:        cred,
			Secure:       useSSL,
			Region:       bc.Region,
			BucketLookup: bucketLookup,
		})
		if err != nil {
			log.Infof("failed to init s3 client server: %v, retried %d times", err, retries)
			if retries >= s3ServerRetries {
				return nil, fmt.Errorf("failed to set s3 server: %v", err)
			}
			continue
		}
		if bc.EndpointCA != "" {
			tr, err = setTransportCA(tr, bc.EndpointCA)
			if err != nil {
				return nil, err
			}
		}
		client.SetCustomTransport(tr)

		break
	}

	found, err := client.BucketExists(bc.BucketName)
	if err != nil {
		return nil, fmt.Errorf("failed to check s3 bucket:%s, err:%v", bc.BucketName, err)
	}
	if !found {
		return nil, fmt.Errorf("bucket %s is not found", bc.BucketName)
	}
	return client, nil
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
		n, err := svc.FPutObject(bucketName, fileName, filePath, minio.PutObjectOptions{ContentType: contentType})
		if err != nil {
			log.Infof("failed to upload backup file: %v, retried %d times", err, retries)
			if retries >= s3ServerRetries {
				return fmt.Errorf("failed to upload backup file: %v", err)
			}
			continue
		}
		log.Infof("Successfully uploaded [%s] of size [%d]", fileName, n)
		break
	}
	return nil
}

func DownloadFromS3WithPrefix(client *minio.Client, prefix, bucket string) (string, error) {
	var filename string
	doneCh := make(chan struct{})
	defer close(doneCh)

	objectCh := client.ListObjectsV2(bucket, prefix, false, doneCh)
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
		return "", fmt.Errorf("failed to download s3 backup: no backups found")
	}
	// if folder is included, strip it so it doesnt end up in a folder on the host itself
	targetFilename := path.Base(filename)
	targetFileLocation := filepath.Join(os.TempDir(), targetFilename)
	log.Infof("Temporary location of backup file from s3: %v", targetFileLocation)
	var object *minio.Object
	var err error
	for retries := 0; retries <= s3ServerRetries; retries++ {
		object, err = client.GetObject(bucket, filename, minio.GetObjectOptions{})
		if err != nil {
			log.Infof("Failed to download backup file [%s]: %v, retried %d times", filename, err, retries)
			if retries >= s3ServerRetries {
				return "", fmt.Errorf("unable to download backup file for [%s]: %v", filename, err)
			}
		}
		log.Infof("Successfully downloaded [%s]", filename)
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

func setTransportCA(tr http.RoundTripper, endpointCA string) (http.RoundTripper, error) {
	ca, err := readS3EndpointCA(endpointCA)
	if err != nil {
		return tr, err
	}
	if !isValidCertificate(ca) {
		return tr, fmt.Errorf("s3-endpoint-ca is not a valid x509 certificate")
	}
	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM(ca)

	tr.(*http.Transport).TLSClientConfig = &tls.Config{
		RootCAs: certPool,
	}

	return tr, nil
}

func readS3EndpointCA(endpointCA string) ([]byte, error) {
	// I expect the CA to be passed as base64 string OR a file system path.
	// I do this to be able to pass it through rke/rancher api without writing it
	// to the backup container filesystem.
	ca, err := base64.StdEncoding.DecodeString(endpointCA)
	if err == nil {
		log.Debug("reading s3-endpoint-ca as a base64 string")
	} else {
		ca, err = ioutil.ReadFile(endpointCA)
		log.Debugf("reading s3-endpoint-ca from [%v]", endpointCA)
	}
	return ca, err
}

func isValidCertificate(c []byte) bool {
	p, _ := pem.Decode(c)
	if p == nil {
		return false
	}
	_, err := x509.ParseCertificates(p.Bytes)
	if err != nil {
		return false
	}
	return true
}
