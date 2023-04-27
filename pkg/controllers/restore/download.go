package restore

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/backup-restore-operator/pkg/objectstore"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value"
)

func (h *handler) downloadFromS3(restore *v1.Restore, objStore *v1.S3ObjectStore) (string, error) {
	s3Client, err := objectstore.GetS3Client(h.ctx, objStore, h.dynamicClient)
	if err != nil {
		return "", err
	}
	prefix := restore.Spec.BackupFilename
	if len(prefix) == 0 {
		return "", fmt.Errorf("empty backup name")
	}
	folder := objStore.Folder
	if len(folder) != 0 {
		// remove the trailing / from the folder name
		prefix = fmt.Sprintf("%s/%s", strings.TrimSuffix(folder, "/"), prefix)
	}
	targetFileLocation, err := objectstore.DownloadFromS3WithPrefix(s3Client, prefix, objStore.BucketName)
	if err != nil {
		return "", err
	}
	return targetFileLocation, nil
}

// very initial parts: https://medium.com/@skdomino/taring-untaring-files-in-go-6b07cf56bc07
func (h *handler) LoadFromTarGzip(tarGzFilePath string, transformerMap map[schema.GroupResource]value.Transformer,
	cr *ObjectsFromBackupCR) error {
	r, err := os.Open(tarGzFilePath)
	if err != nil {
		return fmt.Errorf("error opening tarball backup file %v", err)
	}

	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	tarball := tar.NewReader(gz)

	for {
		tarContent, err := tarball.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if tarContent.Typeflag != tar.TypeReg {
			continue
		}
		readData, err := ioutil.ReadAll(tarball)
		if err != nil {
			return err
		}
		if strings.Contains(tarContent.Name, "filters") {
			if strings.Contains(tarContent.Name, "filters.json") {
				if err := json.Unmarshal(readData, &cr.backupResourceSet); err != nil {
					return fmt.Errorf("error unmarshaling backup filters file: %v", err)
				}
			}
			continue
		}

		// tarContent.Name = serviceaccounts.#v1/cattle-system/cattle.json OR users.management.cattle.io#v3/u-lqx8j.json
		err = h.loadDataFromFile(tarContent, readData, transformerMap, cr)
		if err != nil {
			return err
		}
	}
}

func (h *handler) loadDataFromFile(tarContent *tar.Header, readData []byte,
	transformerMap map[schema.GroupResource]value.Transformer, cr *ObjectsFromBackupCR) error {
	var name, namespace, additionalAuthenticatedData string

	cr.resourcesFromBackup[tarContent.Name] = true
	splitPath := strings.Split(tarContent.Name, "/")
	if len(splitPath) == 2 {
		// cluster scoped resource, since no subdir for namespace
		name = strings.TrimSuffix(splitPath[1], ".json")
		additionalAuthenticatedData = name
	} else {
		// namespaced resource, splitPath[0] =  serviceaccounts.#v1, splitPath[1] = namespace
		name = strings.TrimSuffix(splitPath[2], ".json")
		namespace = splitPath[1]
		additionalAuthenticatedData = fmt.Sprintf("%s#%s", namespace, name)
	}
	gvrStr := splitPath[0]
	gvr := getGVR(gvrStr)

	decryptionTransformer := transformerMap[gvr.GroupResource()]
	if decryptionTransformer != nil {
		var encryptedBytes []byte
		if err := json.Unmarshal(readData, &encryptedBytes); err != nil {
			logrus.Errorf("Error unmarshaling encrypted data for resource [%v]: %v", gvr.GroupResource(), err)
			return fmt.Errorf("error unmarshaling encrypted data for resource [%v]: %v", gvr.GroupResource(), err)
		}
		decrypted, _, err := decryptionTransformer.TransformFromStorage(h.ctx, encryptedBytes, value.DefaultContext(additionalAuthenticatedData))
		if err != nil {
			logrus.Errorf("Error decrypting encrypted resource [%v]: %v, provide same encryption config as used for backup", gvr.GroupResource(), err)
			return fmt.Errorf("error decrypting encrypted resource [%v]: %v, provide same encryption config as used for backup", gvr.GroupResource(), err)
		}
		readData = decrypted
	}
	fileMap := make(map[string]interface{})
	err := json.Unmarshal(readData, &fileMap)
	if err != nil {
		if strings.Contains(err.Error(), "json: cannot unmarshal string into Go value") && decryptionTransformer == nil {
			// This will be the case if we try to unmarshal an encrypted resource without decrypting it first
			logrus.Errorf("Error unmarshaling encrypted resource [%v], no encryption config provided ", gvr.GroupResource())
			return fmt.Errorf("error unmarshaling encrypted resource [%v], no encryption config provided", gvr.GroupResource())
		}
		return err
	}
	info := objInfo{
		Name:       name,
		GVR:        gvr,
		ConfigPath: tarContent.Name,
	}
	if strings.EqualFold(gvr.Resource, "customresourcedefinitions") {
		cr.crdInfoToData[info] = unstructured.Unstructured{Object: fileMap}
	} else {
		if namespace != "" {
			info.Namespace = namespace
			cr.namespacedResourceInfoToData[info] = unstructured.Unstructured{Object: fileMap}
		} else {
			cr.clusterscopedResourceInfoToData[info] = unstructured.Unstructured{Object: fileMap}
		}
	}
	return nil
}
