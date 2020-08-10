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

	v1 "github.com/mrajashree/backup/pkg/apis/resources.cattle.io/v1"
	util "github.com/mrajashree/backup/pkg/controllers"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value"
)

func (h *handler) downloadFromS3(restore *v1.Restore) (string, error) {
	objStore := restore.Spec.StorageLocation.S3
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
	secrets := h.dynamicClient.Resource(gvr)
	secretNs, secretName := "default", objStore.CredentialSecretName
	if strings.Contains(objStore.CredentialSecretName, "/") {
		split := strings.SplitN(objStore.CredentialSecretName, "/", 2)
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
	s3Client, err := util.SetS3Service(objStore, accessKey, secretKey, false)
	if err != nil {
		return "", err
	}
	prefix := restore.Spec.BackupFilename
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

// initial parts: https://medium.com/@skdomino/taring-untaring-files-in-go-6b07cf56bc07
func (h *handler) LoadFromTarGzip(tarGzFilePath string, transformerMap map[schema.GroupResource]value.Transformer) ([]v1.ResourceSelector, error) {
	var additionalAuthenticatedData, name, namespace string
	var resourceSelectors []v1.ResourceSelector
	r, err := os.Open(tarGzFilePath)
	if err != nil {
		return resourceSelectors, fmt.Errorf("error opening tar.gz backup fike %v", err)
	}

	gz, err := gzip.NewReader(r)
	if err != nil {
		return resourceSelectors, err
	}
	tarball := tar.NewReader(gz)

	for {
		tarContent, err := tarball.Next()
		if err == io.EOF {
			return resourceSelectors, nil
		}
		if err != nil {
			return resourceSelectors, err
		}
		if tarContent.Typeflag != tar.TypeReg {
			continue
		}
		readData, err := ioutil.ReadAll(tarball)
		if err != nil {
			return resourceSelectors, err
		}
		// tarContent.Name = serviceaccounts.#v1/cattle-system/cattle.json OR users.management.cattle.io#v3/u-lqx8j.json
		fmt.Printf("\nProcessing %v\n", tarContent.Name)
		if strings.Contains(tarContent.Name, "filters") {
			// TODO: generate resourceSet and statussubresource map
			if strings.Contains(tarContent.Name, "filters.json") {
				if err := json.Unmarshal(readData, &resourceSelectors); err != nil {
					return resourceSelectors, fmt.Errorf("error unmarshaling backup filters file: %v", err)
				}
			}
			if strings.Contains(tarContent.Name, "statussubresource.json") {
				if err := json.Unmarshal(readData, &h.resourcesWithStatusSubresource); err != nil {
					return resourceSelectors, fmt.Errorf("error unmarshaling backup filters file: %v", err)
				}
			}
			continue
		}
		h.resourcesFromBackup[tarContent.Name] = true
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
				return resourceSelectors, err
			}
			decrypted, _, err := decryptionTransformer.TransformFromStorage(encryptedBytes, value.DefaultContext(additionalAuthenticatedData))
			if err != nil {
				return resourceSelectors, err
			}
			readData = decrypted
		}
		fileMap := make(map[string]interface{})
		err = json.Unmarshal(readData, &fileMap)
		if err != nil {
			return resourceSelectors, err
		}
		info := objInfo{
			Name:       name,
			GVR:        gvr,
			ConfigPath: tarContent.Name,
		}
		if strings.EqualFold(gvr.Resource, "customresourcedefinitions") {
			h.crdInfoToData[info] = unstructured.Unstructured{Object: fileMap}
		} else if strings.EqualFold(gvr.Resource, "namespaces") {
			h.namespaceInfoToData[info] = unstructured.Unstructured{Object: fileMap}
		} else {
			if namespace != "" {
				info.Namespace = namespace
			}
			h.resourceInfoToData[info] = unstructured.Unstructured{Object: fileMap}
		}

		// unsetting namespace for the next resource
		namespace = ""
	}
}
