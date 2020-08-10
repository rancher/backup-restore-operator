package restore

import (
	"k8s.io/client-go/dynamic"
	"path/filepath"
	"time"

	v1 "github.com/mrajashree/backup/pkg/apis/resources.cattle.io/v1"
	util "github.com/mrajashree/backup/pkg/controllers"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value"
)

type pruneResourceInfo struct {
	name      string
	namespace string
	gvr       schema.GroupVersionResource
}

func (h *handler) prune(resourceSelectors []v1.ResourceSelector, transformerMap map[schema.GroupResource]value.Transformer,
	deleteTimeout int) error {
	rh := util.ResourceHandler{
		DiscoveryClient: h.discoveryClient,
		DynamicClient:   h.dynamicClient,
		TransformerMap:  transformerMap,
	}

	if _, err := rh.GatherResources(h.ctx, resourceSelectors); err != nil {
		return err
	}
	var resourcesToDelete []pruneResourceInfo
	for gvResource, resObjects := range rh.GVResourceToObjects {
		for _, resObj := range resObjects {
			metadata := resObj.Object["metadata"].(map[string]interface{})
			objName := metadata["name"].(string)
			objNs, _ := metadata["namespace"].(string)
			gv := gvResource.GroupVersion
			resourcePath := gvResource.Name + "." + gv.Group + "#" + gv.Version
			if gvResource.Namespaced {
				resourcePath = filepath.Join(resourcePath, objNs)
			}
			resourceFilePath := filepath.Join(resourcePath, objName+".json")
			logrus.Infof("resourceFilePath: %v", resourceFilePath)
			if !h.resourcesFromBackup[resourceFilePath] {
				resourcesToDelete = append(resourcesToDelete, pruneResourceInfo{
					name:      objName,
					namespace: objNs,
					gvr:       gv.WithResource(gvResource.Name),
				})
			}
		}
	}
	logrus.Infof("Now Need to delete following resources %v", resourcesToDelete)
	clusterScopedResourceDeletionError := make(chan error, 1)
	logrus.Infof("Pruning  resources")
	go h.pruneClusterScopedResources(resourcesToDelete, deleteTimeout, clusterScopedResourceDeletionError)
	cErr := <-clusterScopedResourceDeletionError
	if cErr != nil {
		return cErr
	}
	logrus.Infof("Returning")
	return nil
}

func (h *handler) pruneClusterScopedResources(resourcesToDelete []pruneResourceInfo, pruneTimeout int, retErr chan error) {
	if err := h.deleteResources(resourcesToDelete, false); err != nil {
		retErr <- err
		return
	}
	logrus.Infof("Will retry deleting resources by removing finalizers")
	time.Sleep(time.Duration(pruneTimeout) * time.Second)
	logrus.Infof("Retrying deleting resources by removing finalizers")
	if err := h.deleteResources(resourcesToDelete, true); err != nil {
		retErr <- err
		return
	}
	retErr <- nil
}

func (h *handler) deleteResources(resourcesToDelete []pruneResourceInfo, removeFinalizers bool) error {
	var errgrp errgroup.Group
	resourceQueue := util.GetObjectQueue(resourcesToDelete, len(resourcesToDelete))

	for w := 0; w < util.WorkerThreads; w++ {
		errgrp.Go(func() error {
			var errList []error
			for res := range resourceQueue {
				resource := res.(pruneResourceInfo)
				var dr dynamic.ResourceInterface
				dr = h.dynamicClient.Resource(resource.gvr)
				if resource.namespace != "" {
					dr = h.dynamicClient.Resource(resource.gvr).Namespace(resource.namespace)
				}
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
				}
			}
			return util.ErrList(errList)
		})
	}
	close(resourceQueue)
	return errgrp.Wait()
}
