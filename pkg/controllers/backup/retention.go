package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/s3utils"
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
			return h.deleteBackupsFromMountPath(retentionCount, h.defaultBackupMountPath, backup.Name, backup.Spec.EncryptionConfigSecretName != "")
		} else if h.defaultS3BackupLocation != nil {
			// not checking for nil, since if this wasn't provided, the default local location would get used
			s3Client, err := objectstore.GetS3Client(h.ctx, h.defaultS3BackupLocation, h.dynamicClient)
			if err != nil {
				return err
			}
			return h.deleteS3Backups(backup, h.defaultS3BackupLocation, s3Client, retentionCount, backup.Spec.EncryptionConfigSecretName != "")
		}
	} else if backup.Spec.StorageLocation.S3 != nil {
		s3Client, err := objectstore.GetS3Client(h.ctx, backup.Spec.StorageLocation.S3, h.dynamicClient)
		if err != nil {
			return err
		}
		return h.deleteS3Backups(backup, backup.Spec.StorageLocation.S3, s3Client, retentionCount, backup.Spec.EncryptionConfigSecretName != "")
	}
	return nil
}

func (h *handler) deleteBackupsFromMountPath(retentionCount int, backupLocation, name string, encrypted bool) error {
	var fileMatchPattern string
	if encrypted {
		fileMatchPattern = filepath.Join(backupLocation, fmt.Sprintf("%s-%s*.tar.gz.enc", name, h.kubeSystemNS))
	} else {
		fileMatchPattern = filepath.Join(backupLocation, fmt.Sprintf("%s-%s*.tar.gz", name, h.kubeSystemNS))
	}
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
		if err := os.Remove(filepath.Join(backupLocation, file.filename)); err != nil {
			return err
		}
	}
	return nil
}

func (h *handler) deleteS3Backups(backup *v1.Backup, s3 *v1.S3ObjectStore, svc *minio.Client, retentionCount int, encrypted bool) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	prefix := ""
	isRecursive := false
	if len(s3.Folder) != 0 {
		prefix = s3.Folder
		// Recurse will show us the files in the folder
		isRecursive = true
	}
	opts := minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: isRecursive,
	}
	if s3utils.IsGoogleEndpoint(*svc.EndpointURL()) {
		logrus.Info("Endpoint is Google GCS")
		opts.UseV1 = true
	}

	objectCh := svc.ListObjects(ctx, s3.BucketName, opts)
	// default-backup-([a-z0-9-]).*([tar]).gz
	// default-test-ecm-backup-24e1b8ce-1f00-4bbe-94bb-248ad7606dc8-([0-9-#]).*tar.gz$ OR
	// default-test-ecm-backup-24e1b8ce-1f00-4bbe-94bb-248ad7606dc8-([0-9-#]).*tar.gz.enc$
	re := backupFileRegexp(backup.Name, h.kubeSystemNS, encrypted)
	var backupFiles []backupInfo
	for object := range objectCh {
		if object.Err != nil {
			logrus.Error("error to fetch s3 file:", object.Err)
			return object.Err
		}
		// only keep the files of this backup
		if !re.MatchString(backupFileName(object.Key, prefix)) {
			continue
		}
		b := backupInfo{
			filename:          object.Key,
			creationTimestamp: object.LastModified,
		}
		backupFiles = append(backupFiles, b)
	}
	if len(backupFiles) <= retentionCount {
		return nil
	}
	sort.Slice(backupFiles, func(i, j int) bool {
		return !backupFiles[i].creationTimestamp.Before(backupFiles[j].creationTimestamp)
	})
	for _, backupFile := range backupFiles[retentionCount:] {
		logrus.Infof("Deleting s3 backup file [%s] to follow retention policy of max %v backups", backupFile.filename, retentionCount)
		err := svc.RemoveObject(context.Background(), s3.BucketName, backupFile.filename, minio.RemoveObjectOptions{})
		if err != nil {
			logrus.Errorf("Error detected during deletion: %v", err)
			return err
		}
		logrus.Infof("Success delete s3 backup file [%s]", backupFile.filename)
	}
	return nil
}

// backupFileRegexp matches the files of one backup, which are named
// <backup name>-<kube-system namespace uid>-<timestamp>.tar.gz, with .enc
// appended when the backup is encrypted. The name is quoted and the match
// is anchored at the start, so a backup whose name is a suffix of another
// backup's name, or contains regular expression characters, only sees its
// own files.
func backupFileRegexp(backupName, kubeSystemNS string, encrypted bool) *regexp.Regexp {
	suffix := `[0-9-#].*\.tar\.gz$`
	if encrypted {
		suffix = `[0-9-#].*\.tar\.gz\.enc$`
	}
	return regexp.MustCompile("^" + regexp.QuoteMeta(backupName+"-"+kubeSystemNS+"-") + suffix)
}

// backupFileName strips the configured folder from an object key so the file
// name can be matched from its start. Keys outside that folder, including
// nested folders and folders that merely share the prefix, keep their path
// and therefore do not match.
func backupFileName(key, folder string) string {
	if folder == "" {
		return key
	}
	return strings.TrimPrefix(key, folder+"/")
}
