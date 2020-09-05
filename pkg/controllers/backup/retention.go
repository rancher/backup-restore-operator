package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v6"
	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/backup-restore-operator/pkg/objectstore"
	"github.com/sirupsen/logrus"
)

type backupInfo struct {
	filename          string
	creationTimestamp time.Time
}

func (h *handler) deleteBackupsFollowingRetentionPolicy(backup *v1.Backup) error {
	retentionCount := int(backup.Spec.RetentionCount)
	if backup.Spec.StorageLocation == nil {
		if h.defaultBackupMountPath != "" {
			return h.deleteBackupsFromMountPath(retentionCount, h.defaultBackupMountPath, backup.Name)
		} else if h.defaultS3BackupLocation != nil {
			// not checking for nil, since if this wasn't provided, the default local location would get used
			s3Client, err := objectstore.GetS3Client(h.ctx, h.defaultS3BackupLocation, h.dynamicClient)
			if err != nil {
				return err
			}
			return h.deleteS3Backups(backup, h.defaultS3BackupLocation, s3Client, retentionCount)
		}
	} else if backup.Spec.StorageLocation.S3 != nil {
		s3Client, err := objectstore.GetS3Client(h.ctx, backup.Spec.StorageLocation.S3, h.dynamicClient)
		if err != nil {
			return err
		}
		return h.deleteS3Backups(backup, backup.Spec.StorageLocation.S3, s3Client, retentionCount)
	}
	return nil
}

func (h *handler) deleteBackupsFromMountPath(retentionCount int, backupLocation, name string) error {
	fileMatchPattern := filepath.Join(backupLocation, fmt.Sprintf("%s-%s*.tar.gz", name, h.kubeSystemNS))
	logrus.Infof("Finding files starting with %v", fileMatchPattern)
	fileMatches, err := filepath.Glob(fileMatchPattern)
	if err != nil {
		return err
	}
	if len(fileMatches) <= retentionCount {
		return nil
	}
	var backupFiles []backupInfo
	for _, file := range fileMatches {
		fileInfo, err := os.Stat(file)
		if err != nil {
			logrus.Errorf("Error getting file information for %v: %v", file, err)
			continue
		}
		b := backupInfo{
			filename:          fileInfo.Name(),
			creationTimestamp: fileInfo.ModTime(),
		}
		backupFiles = append(backupFiles, b)
	}
	sort.Slice(backupFiles, func(i, j int) bool {
		return !backupFiles[i].creationTimestamp.Before(backupFiles[j].creationTimestamp)
	})
	for _, file := range backupFiles[retentionCount:] {
		logrus.Infof("File %v was created at %v, deleting it to follow backup's policy of retaining %v backups", file.filename, file.creationTimestamp, retentionCount)
		if err := os.Remove(file.filename); err != nil {
			return err
		}
	}
	return nil
}

func (h *handler) deleteS3Backups(backup *v1.Backup, s3 *v1.S3ObjectStore, svc *minio.Client, retentionCount int) error {
	// Create a done channel to control 'ListObjectsV2' go routine.
	doneCh := make(chan struct{})

	// Indicate to our routine to exit cleanly upon return.
	defer close(doneCh)

	isRecursive := false
	prefix := ""
	if len(s3.Folder) != 0 {
		prefix = s3.Folder
		// Recurse will show us the files in the folder
		isRecursive = true
	}
	objectCh := svc.ListObjects(s3.BucketName, prefix, isRecursive, doneCh)
	// default-backup-([a-z0-9-]).*([tar]).gz
	// default-test-ecm-backup-24e1b8ce-1f00-4bbe-94bb-248ad7606dc8-([0-9-#]).*tar.gz$
	re := regexp.MustCompile(fmt.Sprintf("%s-%s-([0-9-#]).*tar.gz$", backup.Name, h.kubeSystemNS))
	var backupFiles []backupInfo
	for object := range objectCh {
		if object.Err != nil {
			logrus.Error("error to fetch s3 file:", object.Err)
			return object.Err
		}
		// only parse backup file names that matches backup format
		if re.MatchString(object.Key) {
			filename := object.Key

			if len(s3.Folder) != 0 {
				// example object.Key with folder: folder/timestamp_etcd.zip
				// folder and separator needs to be stripped so time can be parsed below
				logrus.Debugf("Stripping [%s] from [%s]", fmt.Sprintf("%s/", prefix), filename)
				filename = strings.TrimPrefix(filename, fmt.Sprintf("%s/", prefix))
			}
			b := backupInfo{
				filename:          object.Key,
				creationTimestamp: object.LastModified,
			}
			backupFiles = append(backupFiles, b)
		}
	}
	if len(backupFiles) <= retentionCount {
		return nil
	}
	sort.Slice(backupFiles, func(i, j int) bool {
		return !backupFiles[i].creationTimestamp.Before(backupFiles[j].creationTimestamp)
	})
	for _, backupFile := range backupFiles[retentionCount:] {
		logrus.Infof("Deleting s3 backup file [%s] to follow retention policy of max %v backups", backupFile.filename, retentionCount)
		err := svc.RemoveObject(s3.BucketName, backupFile.filename)
		if err != nil {
			logrus.Errorf("Error detected during deletion: %v", err)
			return err
		}
		logrus.Infof("Success delete s3 backup file [%s]", backupFile.filename)
	}
	return nil
}
