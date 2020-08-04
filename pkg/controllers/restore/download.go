package restore

import (
	"fmt"
	"strings"

	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	util "github.com/mrajashree/backup/pkg/controllers"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (h *handler) downloadFromS3(restore *v1.Restore) (string, error) {
	objStore := restore.Spec.ObjectStore
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
	secrets := h.dynamicClient.Resource(gvr)
	secretNs, secretName := "default", objStore.Credentials
	if strings.Contains(objStore.Credentials, "/") {
		split := strings.SplitN(objStore.Credentials, "/", 2)
		if len(split) != 2 {
			return "", fmt.Errorf("invalid credentials secret info")
		}
		secretNs = split[0]
		secretName = split[1]
	}
	s3secret, err := secrets.Namespace(secretNs).Get(h.ctx, secretName, k8sv1.GetOptions{})
	if err != nil {
		return "", err
	}
	s3SecretData, ok := s3secret.Object["data"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("malformed secret")
	}
	accessKey, _ := s3SecretData["accessKey"].(string)
	secretKey, _ := s3SecretData["secretKey"].(string)
	s3Client, err := util.SetS3Service(restore.Spec.ObjectStore, accessKey, secretKey, false)
	if err != nil {
		return "", err
	}
	prefix := restore.Spec.BackupFileName
	if len(prefix) == 0 {
		return "", fmt.Errorf("empty backup name")
	}
	folder := objStore.Folder
	if len(folder) != 0 {
		prefix = fmt.Sprintf("%s/%s", folder, prefix)
	}
	targetFileLocation, err := util.DownloadFromS3WithPrefix(s3Client, prefix, objStore.BucketName)
	if err != nil {
		return "", err
	}
	return targetFileLocation, nil
}
