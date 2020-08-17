package restore

import (
	"path/filepath"
	"strings"
	"time"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/backup-restore-operator/pkg/resourcesets"
	"github.com/rancher/backup-restore-operator/pkg/util"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value"
	"k8s.io/client-go/dynamic"
)

type pruneResourceInfo struct {
	name      string
	namespace string
	gvr       schema.GroupVersionResource
}

func (h *handler) prune(resourceSelectors []v1.ResourceSelector, transformerMap map[schema.GroupResource]value.Transformer,
	deleteTimeout int) error {
	var resourcesToDelete []pruneResourceInfo
	rh := resourcesets.ResourceHandler{
		DiscoveryClient: h.discoveryClient,
		DynamicClient:   h.dynamicClient,
		TransformerMap:  transformerMap,
	}

	if _, err := rh.GatherResources(h.ctx, resourceSelectors); err != nil {
		return err
	}

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
				logrus.Infof("Marking resource %v for deletion", strings.TrimSuffix(resourceFilePath, ".json"))
				resourcesToDelete = append(resourcesToDelete, pruneResourceInfo{
					name:      objName,
					namespace: objNs,
					gvr:       gv.WithResource(gvResource.Name),
				})
			}
		}
	}
	logrus.Infof("Pruning following resources %v", resourcesToDelete)
	return h.pruneClusterScopedResources(resourcesToDelete, deleteTimeout)
}

func (h *handler) pruneClusterScopedResources(resourcesToDelete []pruneResourceInfo, pruneTimeout int) error {
	err := h.deleteResources(resourcesToDelete, false)
	if err != nil {
		// don't return this error, let the second call retry
		logrus.Errorf("Error pruning resources: %v", err)
	}

	logrus.Infof("Will retry pruning resources by removing finalizers in %vs", pruneTimeout)
	time.Sleep(time.Duration(pruneTimeout) * time.Second)
	logrus.Infof("Retrying pruning resources by removing finalizers")
	return h.deleteResources(resourcesToDelete, true)
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
