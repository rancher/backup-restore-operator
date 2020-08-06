package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	"github.com/sirupsen/logrus"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type ResourceHandler struct {
	DiscoveryClient discovery.DiscoveryInterface
	DynamicClient   dynamic.Interface
}

func (h *ResourceHandler) GatherResources(ctx context.Context, filters []v1.BackupFilter, backupPath string, transformerMap map[schema.GroupResource]value.Transformer) error {
	var resourceVersion string
	for _, filter := range filters {
		resourceList, err := h.gatherResourcesForGroupVersion(filter)
		if err != nil {
			return err
		}

		gv, err := schema.ParseGroupVersion(filter.ApiGroup)
		if err != nil {
			return err
		}

		for _, res := range resourceList {
			if skipBackup(res) {
				fmt.Printf("\nskipping backup for %#v\n", res)
				continue
			}
			fmt.Printf("\ncurr res: %#v\n", res)
			if resourceVersion == "" {
				gvr := gv.WithResource(res.Name)
				var dr dynamic.ResourceInterface
				dr = h.DynamicClient.Resource(gvr)
				resList, err := dr.List(ctx, k8sv1.ListOptions{})
				if err != nil {
					return err
				}
				resourceVersion = resList.GetResourceVersion()
				logrus.Infof("resourceVersion first try using func: %v\n", resourceVersion)
				logrus.Infof("resourceVersion first try using func: %v\n", resList.Object["metadata"])
			}

			filteredObjects, err := h.gatherAndWriteObjectsForResource(ctx, res, gv, filter, resourceVersion)
			if err != nil {
				return err
			}
			if err := h.writeBackupObjects(filteredObjects, res, gv, backupPath, transformerMap); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *ResourceHandler) gatherResourcesForGroupVersion(filter v1.BackupFilter) ([]k8sv1.APIResource, error) {
	var resourceList, resourceListFromRegex, resourceListFromNames []k8sv1.APIResource
	groupVersion := filter.ApiGroup

	// TODO: accept all groupVersions in one filter
	//if groupVersion == "*" {
	//	_, resources, err = h.discoveryClient.ServerGroupsAndResources()
	//	if err != nil {
	//		return resourceList, err
	//	}
	//}
	resources, err := h.DiscoveryClient.ServerResourcesForGroupVersion(groupVersion)
	if err != nil {
		return resourceList, err
	}

	// resources list has all resources under given groupVersion, first filter based on KindsRegex
	if filter.KindsRegex != "" {
		if filter.KindsRegex == "." {
			// continue to retrieve everything
			return resources.APIResources, nil
		}
		// else filter out resource with regex match
		for _, res := range resources.APIResources {
			matched, err := regexp.MatchString(filter.KindsRegex, res.Name)
			if err != nil {
				return resourceList, err
			}
			if !matched {
				continue
			}
			logrus.Infof("resource kind %v matched regex %v\n", res.Name, filter.KindsRegex)
			resourceListFromRegex = append(resourceListFromRegex, res)
		}
	}

	// then add the resources from Kinds field to this list
	if len(filter.Kinds) > 0 {
		resourceListAfterRegexMatch := make(map[string]bool)
		for _, res := range resourceListFromRegex {
			resourceListAfterRegexMatch[res.Name] = true
		}
		resourceTypesToInclude := make(map[string]bool)
		for _, kind := range filter.Kinds {
			resourceTypesToInclude[kind] = true
		}
		for _, res := range resources.APIResources {
			if resourceTypesToInclude[res.Name] && !resourceListAfterRegexMatch[res.Name] {
				resourceListFromNames = append(resourceListFromNames, res)
			}
		}
	}
	// combine both
	resourceList = append(resourceListFromRegex, resourceListFromNames...)
	return resourceList, nil
}

func (h *ResourceHandler) gatherAndWriteObjectsForResource(ctx context.Context, res k8sv1.APIResource, gv schema.GroupVersion, filter v1.BackupFilter, resourceVersion string) ([]unstructured.Unstructured, error) {
	var filteredByNamespace, filteredObjects []unstructured.Unstructured

	gvr := gv.WithResource(res.Name)
	var dr dynamic.ResourceInterface
	dr = h.DynamicClient.Resource(gvr)

	filteredByName, err := h.filterByNameAndLabel(ctx, dr, filter, resourceVersion)
	if err != nil {
		return filteredObjects, err
	}

	if res.Namespaced {
		if len(filter.Namespaces) > 0 || filter.NamespaceRegex != "" {
			filteredByNamespace, err = h.filterByNamespace(filter, filteredByName)
			if err != nil {
				return filteredObjects, err
			}
			filteredObjects = filteredByNamespace
			return filteredObjects, nil
		}
	}
	filteredObjects = filteredByName
	return filteredObjects, nil
}

func (h *ResourceHandler) filterByNameAndLabel(ctx context.Context, dr dynamic.ResourceInterface, filter v1.BackupFilter, resourceVersion string) ([]unstructured.Unstructured, error) {
	var filteredByName, filteredByResourceNames []unstructured.Unstructured
	var fieldSelector, labelSelector string
	// first get all objects of this resource type
	if filter.LabelSelectors != nil {
		labelMap, err := k8sv1.LabelSelectorAsMap(filter.LabelSelectors)
		if err != nil {
			return filteredByName, err
		}
		labelSelector = labels.SelectorFromSet(labelMap).String()
	}

	logrus.Infof("trying list with resourceversion %v", resourceVersion)
	//resourceObjectsList, err := dr.List(ctx, k8sv1.ListOptions{LabelSelector: labelSelector, ResourceVersion: resourceVersion})
	resourceObjectsList, err := dr.List(ctx, k8sv1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return filteredByName, err
	}
	logrus.Infof("[%v] list resourceObjectsList: %v, org resourceVersion: %v", time.Now(), resourceObjectsList.GetResourceVersion(), resourceVersion)

	filteredByNameMap := make(map[*unstructured.Unstructured]bool)

	if len(filter.ResourceNames) == 0 && filter.ResourceNameRegex == "" {
		// no filters for names of the resource, return all objects from list
		return resourceObjectsList.Items, nil
	}
	// filter out using ResourceNameRegex
	if filter.ResourceNameRegex != "" {
		if filter.ResourceNameRegex == "." {
			// include all resources obtained from list
			return resourceObjectsList.Items, nil
		}

		for _, resObj := range resourceObjectsList.Items {
			metadata := resObj.Object["metadata"].(map[string]interface{})
			name := metadata["name"].(string)
			nameMatched, err := regexp.MatchString(filter.ResourceNameRegex, name)
			if err != nil {
				return filteredByName, err
			}
			if !nameMatched {
				continue
			}
			filteredByName = append(filteredByName, resObj)
			filteredByNameMap[&resObj] = true
		}
	}

	// filter by names as fieldSelector:
	if len(filter.ResourceNames) > 0 {
		for _, name := range filter.ResourceNames {
			fieldSelector += fmt.Sprintf("metadata.name=%s,", name)
		}
		strings.TrimRight(fieldSelector, ",")
		//filteredObjectsList, err := dr.List(ctx, k8sv1.ListOptions{FieldSelector: fieldSelector, LabelSelector: labelSelector,
		//	ResourceVersion: resourceVersion})
		filteredObjectsList, err := dr.List(ctx, k8sv1.ListOptions{FieldSelector: fieldSelector, LabelSelector: labelSelector})
		if err != nil {
			return filteredByName, err
		}
		logrus.Infof("list filteredObjectsList: %v", filteredObjectsList.GetResourceVersion())
		filteredByResourceNames = filteredObjectsList.Items

		if len(filteredByResourceNames) == 0 {
			// none matched
			return filteredByName, nil
		}
		if len(filteredByNameMap) > 0 {
			for _, resObj := range filteredByResourceNames {
				if !filteredByNameMap[&resObj] {
					filteredByName = append(filteredByName, resObj)
				}
			}
		} else {
			filteredByName = filteredByResourceNames
		}

	}
	return filteredByName, nil
}

func (h *ResourceHandler) filterByNamespace(filter v1.BackupFilter, filteredByName []unstructured.Unstructured) ([]unstructured.Unstructured, error) {
	var filteredByNamespace, filteredByNamespaceRegex, filteredObjects []unstructured.Unstructured

	filteredByNsMap := make(map[*unstructured.Unstructured]bool)

	if len(filter.Namespaces) > 0 {
		allowedNamespaces := make(map[string]bool)
		for _, ns := range filter.Namespaces {
			allowedNamespaces[ns] = true
		}
		for _, resObj := range filteredByName {
			metadata := resObj.Object["metadata"].(map[string]interface{})
			ns := metadata["namespace"].(string)
			if allowedNamespaces[ns] {
				filteredByNamespace = append(filteredByNamespace, resObj)
				filteredByNsMap[&resObj] = true
			}
		}
	}
	if filter.NamespaceRegex != "" {
		if filter.NamespaceRegex == "." {
			// include all namespaces, so return all objects obtained after filtering by name
			return filteredByName, nil
		}
		for _, resObj := range filteredByName {
			metadata := resObj.Object["metadata"].(map[string]interface{})
			ns := metadata["namespace"].(string)
			nsMatched, err := regexp.MatchString(filter.NamespaceRegex, ns)
			if err != nil {
				return filteredByNamespace, err
			}
			if !nsMatched {
				continue
			}
			filteredByNamespaceRegex = append(filteredByNamespaceRegex, resObj)
		}

		if len(filteredByNamespaceRegex) == 0 {
			// none matched regex
			return filteredByNamespace, nil
		}

		if len(filteredByNsMap) > 0 {
			for _, resObj := range filteredByNamespaceRegex {
				if !filteredByNsMap[&resObj] {
					filteredByNamespace = append(filteredByNamespace, resObj)
				}
			}
		} else {
			filteredByNamespace = filteredByNamespaceRegex
		}
	}
	filteredObjects = append(filteredByNamespace, filteredByNamespaceRegex...)
	return filteredObjects, nil
}

func (h *ResourceHandler) writeBackupObjects(resObjects []unstructured.Unstructured, res k8sv1.APIResource, gv schema.GroupVersion, backupPath string, transformerMap map[schema.GroupResource]value.Transformer) error {
	for _, resObj := range resObjects {
		metadata := resObj.Object["metadata"].(map[string]interface{})
		// if an object has deletiontimestamp and finalizers, back it up. If there are no finalizers, ignore
		if _, deletionTs := metadata["deletionTimestamp"]; deletionTs {
			if _, finSet := metadata["finalizers"]; !finSet {
				// no finalizers set, don't backup object
				continue
			}
		}

		objName := metadata["name"].(string)

		// TODO: decide whether to store deletionTimestamp or not
		// TODO: check if generation is needed
		for _, field := range []string{"uid", "creationTimestamp", "selfLink", "resourceVersion"} {
			delete(metadata, field)
		}

		gr := schema.ParseGroupResource(res.Name + "." + res.Group)
		encryptionTransformer := transformerMap[gr]
		additionalAuthenticatedData := objName
		//if res.Namespaced {
		//	additionalAuthenticatedData = metadata["namespace"].(string) + "/" + additionalAuthenticatedData
		//}

		resourcePath := backupPath + "/" + res.Name + "." + gv.Group + "#" + gv.Version
		if err := createResourceDir(resourcePath); err != nil {
			return err
		}

		err := writeToBackup(resObj.Object, resourcePath, objName, encryptionTransformer, additionalAuthenticatedData)
		if err != nil {
			return err
		}
	}
	return nil
}

func createResourceDir(path string) error {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		err = os.Mkdir(path, os.ModePerm)
		if err != nil {
			return fmt.Errorf("error creating temp dir: %v", err)
		}
	}
	return nil
}

func writeToBackup(resource map[string]interface{}, backupPath, filename string, transformer value.Transformer, additionalAuthenticatedData string) error {
	f, err := os.Create(filepath.Join(backupPath, filepath.Base(filename+".json")))
	if err != nil {
		return fmt.Errorf("error creating temp file: %v", err)
	}
	defer f.Close()

	resourceBytes, err := json.Marshal(resource)
	if err != nil {
		return fmt.Errorf("error converting resource to JSON: %v", err)
	}
	if transformer != nil {
		encrypted, err := transformer.TransformToStorage(resourceBytes, value.DefaultContext([]byte(additionalAuthenticatedData)))
		if err != nil {
			return fmt.Errorf("error converting resource to JSON: %v", err)
		}
		resourceBytes, err = json.Marshal(encrypted)
		if err != nil {
			return fmt.Errorf("error converting encrypted resource to JSON: %v", err)
		}
	}
	if _, err := f.Write(resourceBytes); err != nil {
		return fmt.Errorf("error writing JSON to file: %v", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("error closing file: %v", err)
	}
	return nil
}

func skipBackup(res k8sv1.APIResource) bool {
	if !canListResource(res.Verbs) {
		logrus.Debugf("Cannot list resource %v, not backing up", res)
		return true
	}
	if !canUpdateResource(res.Verbs) {
		logrus.Debugf("Cannot update resource %v, not backing up", res)
		return true
	}
	return false
}

func canListResource(verbs k8sv1.Verbs) bool {
	for _, v := range verbs {
		if v == "list" {
			return true
		}
	}
	return false
}

func canUpdateResource(verbs k8sv1.Verbs) bool {
	for _, v := range verbs {
		if v == "update" || v == "patch" {
			return true
		}
	}
	return false
}
