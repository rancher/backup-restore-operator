package backup

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/ioutil"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"os"
	"path/filepath"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/backup-restore-operator/pkg/objectstore"
)

func (h *handler) uploadToS3(backupNs string, objectStore *v1.S3ObjectStore, tmpBackupPath, gzipFile string) error {
	var accessKey, secretKey string
	tmpBackupGzipFilepath, err := ioutil.TempDir("", "uploadpath")
	if err != nil {
		return err
	}
	if objectStore.Folder != "" {
		if err := os.Mkdir(filepath.Join(tmpBackupGzipFilepath, objectStore.Folder), os.ModePerm); err != nil {
			return err
		}
		gzipFile = fmt.Sprintf("%s/%s", objectStore.Folder, gzipFile)
	}
	if err := CreateTarAndGzip(tmpBackupPath, tmpBackupGzipFilepath, gzipFile); err != nil {
		return err
	}
	if objectStore.CredentialSecretName != "" {
		gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
		secrets := h.dynamicClient.Resource(gvr)
		secretNs, secretName := backupNs, objectStore.CredentialSecretName
		s3secret, err := secrets.Namespace(secretNs).Get(h.ctx, secretName, k8sv1.GetOptions{})
		if err != nil {
			return err
		}
		s3SecretData, ok := s3secret.Object["data"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("malformed secret")
		}
		accessKey, _ = s3SecretData["accessKey"].(string)
		secretKey, _ = s3SecretData["secretKey"].(string)
	}
	// if no s3 credentials are provided, use IAM profile, this means passing empty access and secret keys to the SetS3Service call
	s3Client, err := objectstore.SetS3Service(objectStore, accessKey, secretKey, false)
	if err != nil {
		return err
	}
	if err := objectstore.UploadBackupFile(s3Client, objectStore.BucketName, gzipFile, filepath.Join(tmpBackupGzipFilepath, gzipFile)); err != nil {
		return err
	}
	err = os.RemoveAll(tmpBackupGzipFilepath)
	return err
}

func CreateTarAndGzip(backupPath, targetGzipPath, targetGzipFile string) error {
	gzipFile, err := os.Create(filepath.Join(targetGzipPath, targetGzipFile))
	if err != nil {
		return fmt.Errorf("error creating backup tar gzip file: %v", err)
	}
	// writes to gw will be compressed and written to gzipFile
	gw := gzip.NewWriter(gzipFile)
	defer gw.Close()
	// writes to tw will be written to gw
	tw := tar.NewWriter(gw)
	defer tw.Close()
	walkFunc := func(currPath string, info os.FileInfo, err error) error {
		if currPath == backupPath {
			return nil
		}
		if err != nil {
			return fmt.Errorf("error in walkFunc for %v: %v", currPath, err)
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("error creating header for %v: %v", info.Name(), err)
		}
		relativePath, err := filepath.Rel(backupPath, currPath)
		if err != nil {
			return fmt.Errorf("error getting relative path for %v: %v", info.Name(), err)
		}
		hdr.Name = filepath.Join(relativePath)
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("error writing header for %v: %v", info.Name(), err)
		}
		if info.IsDir() {
			return nil
		}
		fInfo, err := os.Open(currPath)
		if err != nil {
			return fmt.Errorf("error opening %v: %v", info.Name(), err)
		}
		if _, err := io.Copy(tw, fInfo); err != nil {
			return fmt.Errorf("error copying %v: %v", info.Name(), err)
		}
		fInfo.Close()
		return nil
	}
	if err := filepath.Walk(backupPath, walkFunc); err != nil {
		return err
	}

	return nil
}