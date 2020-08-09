package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	v1 "github.com/mrajashree/backup/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/wrangler/pkg/slice"
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
)

type ResourceHandler struct {
	DiscoveryClient discovery.DiscoveryInterface
	DynamicClient   dynamic.Interface
}

/*  GatherResources iterates over the ResourceSelectors in the given ResourceSet
   	Each ResourceSelector can specify only one apigroupversion, example "v1" or "management.cattle.io/v3"
	ResourceSelector can specify resource types/kinds to backup from this apigroupversion through Kinds and KindsRegex.
	Resources matching Kinds and KindsRegex both will be backed up
	ResourceSelector can also specify names of particular resources of this groupversionkind to backup, using ResourceNames and ResourceNamesRegex
	It can specify namespaces from which to backup these resources through Namespaces and NamespacesRegex
	And it can provide a labelSelector to backup resources of this gvk+name+ns combination containing some label
	For each value that has two fields, for regex and an array of exact names GatherResources performs OR
	But it performs AND for separate selector types, example:
	apiversion: v1
	kinds: namespaces
	resourceNamesRegex: "^cattle-|^p-|^c-|^user-|^u-"
	resourceNames: "local"
	All namespaces that match resourceNamesRegex, also local ns is backed up
*/
func (h *ResourceHandler) GatherResources(ctx context.Context, resourceSelectors []v1.ResourceSelector, backupPath string,
	transformerMap map[schema.GroupResource]value.Transformer) (map[string]bool, error) {

	var resourceVersion string
	resourcesWithStatusSubresource := make(map[string]bool)

	for _, resourceSelector := range resourceSelectors {
		resourceList, err := h.gatherResourcesForGroupVersion(resourceSelector)
		if err != nil {
			return resourcesWithStatusSubresource, err
		}
		gv, err := schema.ParseGroupVersion(resourceSelector.ApiGroup)
		if err != nil {
			return resourcesWithStatusSubresource, err
		}
		for _, res := range resourceList {
			// this is a subresource, check if its a status subsubresource
			split := strings.SplitN(res.Name, "/", 2)
			if len(split) == 2 {
				if split[1] == "status" && split[0] != "customresourcedefinitions" && slice.ContainsString(res.Verbs, "update") {
					// this resource has status subresource and it accepts "update" verb, so we need to call UpdateStatus on it during restore
					// we need to save names of such objects
					resourcesWithStatusSubresource[gv.WithResource(split[0]).String()] = true
				}
				// no need to save contents of any subresource as they are a part of the resource
				continue
			}

			// check for all rancher objects
			if skipBackup(res) {
				continue
			}

			filteredObjects, err := h.gatherObjectsForResource(ctx, res, gv, resourceSelector, resourceVersion)
			if err != nil {
				return resourcesWithStatusSubresource, err
			}
			if err := h.writeBackupObjects(filteredObjects, res, gv, backupPath, transformerMap); err != nil {
				return resourcesWithStatusSubresource, err
			}
		}
	}
	return resourcesWithStatusSubresource, nil
}

func (h *ResourceHandler) gatherResourcesForGroupVersion(filter v1.ResourceSelector) ([]k8sv1.APIResource, error) {
	var resourceList, resourceListFromRegex, resourceListFromNames []k8sv1.APIResource
	groupVersion := filter.ApiGroup

	// first list all resources for given groupversion using discovery API
	resources, err := h.DiscoveryClient.ServerResourcesForGroupVersion(groupVersion)
	if err != nil {
		return resourceList, err
	}
	if filter.KindsRegex == "" && len(filter.Kinds) == 0 {
		// if no filters for resource kind are given, return entire resource list
		return resources.APIResources, nil
	}

	// "resources" list has all resources under given groupVersion, first filter based on KindsRegex
	if filter.KindsRegex != "" {
		if filter.KindsRegex == "." {
			// "." will match anything, so return entire resource list
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
		// avoid adding same resource twice by checking what's in resourceListFromRegex
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
	// combine both lists, obtained from regex and from iterating over the kinds list
	resourceList = append(resourceListFromRegex, resourceListFromNames...)
	return resourceList, nil
}

func (h *ResourceHandler) gatherObjectsForResource(ctx context.Context, res k8sv1.APIResource, gv schema.GroupVersion, filter v1.ResourceSelector, resourceVersion string) ([]unstructured.Unstructured, error) {
	var filteredByNamespace, filteredObjects []unstructured.Unstructured

	gvr := gv.WithResource(res.Name)
	var dr dynamic.ResourceInterface
	dr = h.DynamicClient.Resource(gvr)

	// only resources that match name+namespace+label combination will be backed up, so we can filter in any order
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

func (h *ResourceHandler) filterByNameAndLabel(ctx context.Context, dr dynamic.ResourceInterface, filter v1.ResourceSelector, resourceVersion string) ([]unstructured.Unstructured, error) {
	var filteredByName, filteredByResourceNames []unstructured.Unstructured
	var fieldSelector, labelSelector string

	if filter.LabelSelectors != nil {
		labelMap, err := k8sv1.LabelSelectorAsMap(filter.LabelSelectors)
		if err != nil {
			return filteredByName, err
		}
		labelSelector = labels.SelectorFromSet(labelMap).String()
	}

	//resourceObjectsList, err := dr.List(ctx, k8sv1.ListOptions{LabelSelector: labelSelector, ResourceVersion: resourceVersion})
	resourceObjectsList, err := dr.List(ctx, k8sv1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return filteredByName, err
	}
	//logrus.Infof("[%v] list resourceObjectsList: %v, org resourceVersion: %v", time.Now(), resourceObjectsList.GetResourceVersion(), resourceVersion)
	filteredByNameMap := make(map[*unstructured.Unstructured]bool)

	if len(filter.ResourceNames) == 0 && filter.ResourceNameRegex == "" {
		// no filters for names of the resource, return all objects obtained from the list call
		return resourceObjectsList.Items, nil
	}
	// filter out using ResourceNameRegex
	if filter.ResourceNameRegex != "" {
		if filter.ResourceNameRegex == "." {
			// "." will match everything, so return all resources obtained from the list call
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
		// TODO: set resourceVersion later when it becomes clear how to use it
		//filteredObjectsList, err := dr.List(ctx, k8sv1.ListOptions{FieldSelector: fieldSelector, LabelSelector: labelSelector,
		//	ResourceVersion: resourceVersion})
		filteredObjectsList, err := dr.List(ctx, k8sv1.ListOptions{FieldSelector: fieldSelector, LabelSelector: labelSelector})
		if err != nil {
			return filteredByName, err
		}
		filteredByResourceNames = filteredObjectsList.Items

		if len(filteredByResourceNames) == 0 {
			// exact names were provided, but no resources found by that name
			// so return anything obtained from matching resource names by regex
			// if that list is empty too, means nothing matched the filters
			return filteredByName, nil
		}
		if len(filteredByNameMap) > 0 {
			// avoid duplicates
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

func (h *ResourceHandler) filterByNamespace(filter v1.ResourceSelector, filteredByName []unstructured.Unstructured) ([]unstructured.Unstructured, error) {
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
			// "." will match all namespaces, so return all objects obtained after filtering by name
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
			// return whatever was filtered by exact namespace match
			// if that list is also empty, it means no namespaces matched the given filters
			return filteredByNamespace, nil
		}

		if len(filteredByNsMap) > 0 {
			// avoid duplicates
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

func (h *ResourceHandler) writeBackupObjects(resObjects []unstructured.Unstructured, res k8sv1.APIResource,
	gv schema.GroupVersion, backupPath string, transformerMap map[schema.GroupResource]value.Transformer) error {
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
		objFilename := objName

		// TODO: confirm-test deletionTimestamp needs to be dropped
		for _, field := range []string{"uid", "creationTimestamp", "deletionTimestamp", "selfLink", "resourceVersion"} {
			delete(metadata, field)
		}

		resourcePath := backupPath + "/" + res.Name + "." + gv.Group + "#" + gv.Version
		if err := createResourceDir(resourcePath); err != nil {
			return err
		}

		gr := schema.ParseGroupResource(res.Name + "." + res.Group)
		encryptionTransformer := transformerMap[gr]
		additionalAuthenticatedData := objName
		if res.Namespaced {
			additionalAuthenticatedData = fmt.Sprintf("%s#%s", metadata["namespace"].(string), additionalAuthenticatedData)
			/*Max length in k8s is 253 characters for names of resources, for instance for serviceaccount.
			And max length of filename on UNIX is 255, so we risk going over max filename length by storing namespace in the filename,
			hence create a separate subdir for namespaced resources*/
			objNs := metadata["namespace"].(string)
			resourcePath = filepath.Join(resourcePath, objNs)
			if err := createResourceDir(resourcePath); err != nil {
				return err
			}
		}

		// TODO: collect all objects first and then write??
		err := writeToBackup(resObj.Object, resourcePath, objFilename, encryptionTransformer, additionalAuthenticatedData)
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
		logrus.Infof("Cannot list resource %v, not backing up", res)
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
