package backup

import (
	"fmt"
	"io/ioutil"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"os"
	"path/filepath"
	"strings"

	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	util "github.com/mrajashree/backup/pkg/controllers"
)

func (h *handler) uploadToS3(backup *v1.Backup, tmpBackupPath, gzipFile string) error {
	tmpBackupGzipFilepath, err := ioutil.TempDir("", "uploadpath")
	if err != nil {
		return err
	}
	if backup.Spec.ObjectStore.Folder != "" {
		if err := os.Mkdir(filepath.Join(tmpBackupGzipFilepath, backup.Spec.ObjectStore.Folder), os.ModePerm); err != nil {
			return err
		}
		gzipFile = fmt.Sprintf("%s/%s", backup.Spec.ObjectStore.Folder, gzipFile)
	}
	if err := util.CreateTarAndGzip(tmpBackupPath, tmpBackupGzipFilepath, gzipFile); err != nil {
		return err
	}
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
	secrets := h.dynamicClient.Resource(gvr)
	secretNs, secretName := "default", backup.Spec.ObjectStore.Credentials
	if strings.Contains(backup.Spec.ObjectStore.Credentials, "/") {
		split := strings.SplitN(backup.Spec.ObjectStore.Credentials, "/", 2)
		if len(split) != 2 {
			return fmt.Errorf("invalid credentials secret info")
		}
		secretNs = split[0]
		secretName = split[1]
	}
	s3secret, err := secrets.Namespace(secretNs).Get(h.ctx, secretName, k8sv1.GetOptions{})
	if err != nil {
		return err
	}
	s3SecretData, ok := s3secret.Object["data"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("malformed secret")
	}
	accessKey, _ := s3SecretData["accessKey"].(string)
	secretKey, _ := s3SecretData["secretKey"].(string)
	s3Client, err := util.SetS3Service(backup.Spec.ObjectStore, accessKey, secretKey, false)
	if err != nil {
		return err
	}
	if err := util.UploadBackupFile(s3Client, backup.Spec.ObjectStore.BucketName, gzipFile, filepath.Join(tmpBackupGzipFilepath, gzipFile)); err != nil {
		return err
	}
	err = os.RemoveAll(tmpBackupGzipFilepath)
	return err
}
