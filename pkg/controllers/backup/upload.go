package backup

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/backup-restore-operator/pkg/objectstore"
	"github.com/sirupsen/logrus"
)

func (h *handler) uploadToS3(backup *v1.Backup, objectStore *v1.S3ObjectStore, tmpBackupPath, gzipFile string) error {
	tmpBackupGzipFilepath, err := ioutil.TempDir("", "uploadpath")
	if err != nil {
		return err
	}
	if objectStore.Folder != "" {
		if err := os.MkdirAll(filepath.Join(tmpBackupGzipFilepath, objectStore.Folder), os.ModePerm); err != nil {
			return removeTempUploadDir(tmpBackupGzipFilepath, err)
		}
		// we need to avoid both "//" inside the path and all leading and trailing "/"
		gzipFile = fmt.Sprintf("%s/%s", strings.TrimRight(objectStore.Folder, "/"), gzipFile)
		gzipFile = strings.Trim(gzipFile, "/")
	}
	if err := CreateTarAndGzip(tmpBackupPath, tmpBackupGzipFilepath, gzipFile, backup.Name); err != nil {
		return removeTempUploadDir(tmpBackupGzipFilepath, err)
	}
	s3Client, err := objectstore.GetS3Client(h.ctx, objectStore, h.dynamicClient)
	if err != nil {
		return removeTempUploadDir(tmpBackupGzipFilepath, err)
	}
	if err := objectstore.UploadBackupFile(s3Client, objectStore.BucketName, gzipFile, filepath.Join(tmpBackupGzipFilepath, gzipFile)); err != nil {
		return removeTempUploadDir(tmpBackupGzipFilepath, err)
	}
	return os.RemoveAll(tmpBackupGzipFilepath)
}

func CreateTarAndGzip(backupPath, targetGzipPath, targetGzipFile, backupCRName string) error {
	logrus.Infof("Compressing backup CR %v", backupCRName)
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
		// backupPath could be /var/tmp/folders/backup/authconfigs.management.cattle.io/adfs.json
		// we need to include only authconfigs.management.cattle.io onwards, so get relative path
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
		return fInfo.Close()
	}
	return filepath.Walk(backupPath, walkFunc)
}

func removeTempUploadDir(tmpBackupGzipFilepath string, originalErr error) error {
	removeErr := os.RemoveAll(tmpBackupGzipFilepath)
	if removeErr != nil {
		return errors.New(originalErr.Error() + removeErr.Error())
	}
	return originalErr
}
