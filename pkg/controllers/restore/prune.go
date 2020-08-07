package restore

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	util "github.com/mrajashree/backup/pkg/controllers"
	lasso "github.com/rancher/lasso/pkg/client"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value"
)

type resourceInfo struct {
	path string
	name string
	gvr  schema.GroupVersionResource
}

func (h *handler) prune(backupName, backupPath string, pruneTimeout int, transformerMap map[schema.GroupResource]value.Transformer) error {
	// prune
	filtersBytes, err := ioutil.ReadFile(filepath.Join(backupPath, "filters", "filters.json"))
	if err != nil {
		return fmt.Errorf("error reading backup fitlers file: %v", err)
	}
	var backupFilters []v1.ResourceSelector
	if err := json.Unmarshal(filtersBytes, &backupFilters); err != nil {
		return fmt.Errorf("error unmarshaling backup filters file: %v", err)
	}
	rh := util.ResourceHandler{
		DiscoveryClient: h.discoveryClient,
		DynamicClient:   h.dynamicClient,
	}
	pruneDirPath, err := ioutil.TempDir("", fmt.Sprintf("prune-%s", backupName))
	if err != nil {
		return err
	}
	logrus.Infof("Prune dir path is %s", pruneDirPath)

	if _, err := rh.GatherResources(h.ctx, backupFilters, pruneDirPath, transformerMap); err != nil {
		return err
	}
	logrus.Infof("Comparing prune and backup dirs")
	// compare pruneDirPath and backupPath contents, to find any extra files in pruneDirPath, and mark them for deletion
	var namespacedResourcesToDelete []resourceInfo
	var resourcesToDelete []resourceInfo
	walkFunc := func(currPath string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		// check if this file exists in backupPath or not
		// for example, for /var/tmp/authconfigs.management.cattle.io#v3/adfs.json,
		// containingDirFullPath = /var/tmp/authconfigs.management.cattle.io#v3
		containingDirFullPath := path.Dir(currPath)
		// containingDirBasePath = authconfigs.management.cattle.io#v3
		containingDirBasePath := filepath.Base(containingDirFullPath)
		// currFileName = authconfigs.management.cattle.io#v3/adfs.json => removes the path upto the dir for groupversion
		currFileName := filepath.Join(containingDirBasePath, filepath.Base(currPath))
		// if this file does not exist in the backup, it was created after taking backup, so delete it
		if _, err := os.Stat(filepath.Join(backupPath, currFileName)); os.IsNotExist(err) {
			gvr := getGVR(containingDirBasePath)
			isNamespaced, err := lasso.IsNamespaced(gvr, h.restmapper)
			if err != nil {
				logrus.Errorf("Error finding if %v is namespaced: %v", currFileName, err)
			}
			if isNamespaced {
				// use entire path as key, as we need to read this file to get the namespace
				namespacedResourcesToDelete = append(namespacedResourcesToDelete, resourceInfo{
					path: currPath,
					name: "",
					gvr:  gvr,
				})
			} else {
				// use only the filename without json ext as we can delete this resource without reading the file
				resourcesToDelete = append(resourcesToDelete, resourceInfo{
					path: "",
					name: strings.TrimSuffix(filepath.Base(currPath), ".json"),
					gvr:  gvr,
				})
			}

		}
		return nil
	}
	err = filepath.Walk(pruneDirPath, walkFunc)
	if err != nil {
		return err
	}
	logrus.Infof("Now Need to delete namespaced %v", namespacedResourcesToDelete)
	logrus.Infof("Now Need to delete clusterscoped %v", resourcesToDelete)

	if err := h.pruneResources(resourcesToDelete, namespacedResourcesToDelete, pruneTimeout, transformerMap); err != nil {
		if removeErr := os.RemoveAll(pruneDirPath); removeErr != nil {
			return removeErr
		}
		return err
	}
	err = os.RemoveAll(pruneDirPath)
	return err
}

func (h *handler) pruneResources(resourcesToDelete, namespacedResourcesToDelete []resourceInfo, pruneTimeout int,
	transformerMap map[schema.GroupResource]value.Transformer) error {
	clusterScopedResourceDeletionError, namespacedResourceDeleteionError := make(chan error, 1), make(chan error, 1)
	logrus.Infof("Pruning cluster scoped resources")
	go h.pruneClusterScopedResources(resourcesToDelete, pruneTimeout, clusterScopedResourceDeletionError)
	cErr := <-clusterScopedResourceDeletionError
	if cErr != nil {
		return cErr
	}

	logrus.Infof("Pruning namespaced resources")
	go h.pruneNamespacedResources(namespacedResourcesToDelete, pruneTimeout, namespacedResourceDeleteionError, transformerMap)
	nErr := <-namespacedResourceDeleteionError
	if nErr != nil {
		return nErr
	}
	return nil
}

func (h *handler) pruneClusterScopedResources(resourcesToDelete []resourceInfo, pruneTimeout int, retErr chan error) {
	if err := h.deleteClusterScopedResources(resourcesToDelete, false); err != nil {
		retErr <- err
		return
	}
	time.Sleep(time.Duration(pruneTimeout) * time.Second)
	if err := h.deleteClusterScopedResources(resourcesToDelete, true); err != nil {
		retErr <- err
		return
	}
	retErr <- nil
}

func (h *handler) deleteClusterScopedResources(resourcesToDelete []resourceInfo, removeFinalizers bool) error {
	var errgrp errgroup.Group
	resourceQueue := util.GetObjectQueue(resourcesToDelete, len(resourcesToDelete))

	for w := 0; w < util.WorkerThreads; w++ {
		errgrp.Go(func() error {
			var errList []error
			for res := range resourceQueue {
				resource := res.(resourceInfo)
				dr := h.dynamicClient.Resource(resource.gvr)

				if removeFinalizers {
					obj, err := dr.Get(h.ctx, resource.name, k8sv1.GetOptions{})
					if err != nil {
						if apierrors.IsNotFound(err) || apierrors.IsGone(err) {
							continue
						}
						errList = append(errList, err)
						continue
					}
					delete(obj.Object[metadataMapKey].(map[string]interface{}), "finalizers")
					if _, err := dr.Update(h.ctx, obj, k8sv1.UpdateOptions{}); err != nil {
						errList = append(errList, err)
						continue
					}
				}

				if err := dr.Delete(h.ctx, resource.name, k8sv1.DeleteOptions{}); err != nil {
					if apierrors.IsNotFound(err) || apierrors.IsGone(err) {
						continue
					}
					errList = append(errList, err)
					continue
				}
			}
			return util.ErrList(errList)
		})
	}
	close(resourceQueue)
	return errgrp.Wait()
}

func (h *handler) pruneNamespacedResources(resourcesToDelete []resourceInfo, pruneTimeout int, retErr chan error,
	transformerMap map[schema.GroupResource]value.Transformer) {
	if err := h.deleteNamespacedResources(resourcesToDelete, false, transformerMap); err != nil {
		retErr <- err
		return
	}
	time.Sleep(time.Duration(pruneTimeout) * time.Second)
	if err := h.deleteNamespacedResources(resourcesToDelete, true, transformerMap); err != nil {
		retErr <- err
		return
	}
	retErr <- nil
}

func (h *handler) deleteNamespacedResources(resourcesToDelete []resourceInfo, removeFinalizers bool,
	transformerMap map[schema.GroupResource]value.Transformer) error {
	var errgrp errgroup.Group
	resourceQueue := util.GetObjectQueue(resourcesToDelete, len(resourcesToDelete))

	for w := 0; w < util.WorkerThreads; w++ {
		errgrp.Go(func() error {
			var errList []error
			for res := range resourceQueue {
				resource := res.(resourceInfo)

				resourceBytes, err := ioutil.ReadFile(resource.path)
				if err != nil {
					errList = append(errList, err)
					continue
				}

				decryptionTransformer, decrypted := transformerMap[resource.gvr.GroupResource()]
				if decrypted {
					var encryptedBytes []byte
					if err := json.Unmarshal(resourceBytes, &encryptedBytes); err != nil {
						errList = append(errList, err)
						continue
					}
					decrypted, _, err := decryptionTransformer.TransformFromStorage(encryptedBytes, value.DefaultContext(resource.name))
					if err != nil {
						errList = append(errList, err)
						continue
					}
					resourceBytes = decrypted
				}

				var resourceContents map[string]interface{}
				if err := json.Unmarshal(resourceBytes, &resourceContents); err != nil {
					errList = append(errList, err)
					continue
				}
				metadata := resourceContents[metadataMapKey].(map[string]interface{})
				resourceName, nameFound := metadata["name"].(string)
				namespace, nsFound := metadata["namespace"].(string)
				if !nameFound || !nsFound {
					errList = append(errList, fmt.Errorf("cannot delete resource as namespace not found"))
					continue
				}
				dr := h.dynamicClient.Resource(resource.gvr).Namespace(namespace)

				if removeFinalizers {
					obj, err := dr.Get(h.ctx, resourceName, k8sv1.GetOptions{})
					if err != nil {
						if apierrors.IsNotFound(err) || apierrors.IsGone(err) {
							continue
						}
						errList = append(errList, err)
						continue
					}
					delete(obj.Object[metadataMapKey].(map[string]interface{}), "finalizers")
					if _, err := dr.Update(h.ctx, obj, k8sv1.UpdateOptions{}); err != nil {
						errList = append(errList, err)
						continue
					}
				}

				if err := dr.Delete(h.ctx, resourceName, k8sv1.DeleteOptions{}); err != nil {
					if apierrors.IsNotFound(err) || apierrors.IsGone(err) {
						continue
					}
					errList = append(errList, err)
					continue
				}
			}
			return util.ErrList(errList)
		})
	}
	close(resourceQueue)
	return errgrp.Wait()
}
