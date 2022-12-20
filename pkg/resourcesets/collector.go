package resourcesets

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
)

const ListObjectsLimit = 200

type GVResource struct {
	GroupVersion schema.GroupVersion
	Name         string
	Namespaced   bool
}

type ResourceHandler struct {
	DiscoveryClient     discovery.DiscoveryInterface
	DynamicClient       dynamic.Interface
	TransformerMap      map[schema.GroupResource]value.Transformer
	GVResourceToObjects map[GVResource][]unstructured.Unstructured
}

/*
	  GatherResources iterates over the ResourceSelectors in the given ResourceSet
	   	Each ResourceSelector can specify only one apigroupversion, example "v1" or "management.cattle.io/v3"
		ResourceSelector can specify resource types/kinds to backup from this apigroupversion through Kinds and KindsRegexp.
		Resources matching Kinds and KindsRegexp both will be backed up
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
func (h *ResourceHandler) GatherResources(ctx context.Context, resourceSelectors []v1.ResourceSelector) error {
	h.GVResourceToObjects = make(map[GVResource][]unstructured.Unstructured)

	for _, resourceSelector := range resourceSelectors {
		resourceList, err := h.gatherResourcesForGroupVersion(resourceSelector)
		if err != nil {
			return fmt.Errorf("error gathering resource for %v: %v", resourceSelector.APIVersion, err)
		}
		gv, err := schema.ParseGroupVersion(resourceSelector.APIVersion)
		if err != nil {
			return err
		}
		currGVResource := GVResource{GroupVersion: gv}
		for _, res := range resourceList {
			currGVResource.Name = res.Name
			currGVResource.Namespaced = res.Namespaced

			if strings.Contains(res.Name, "/") {
				logrus.Debugf("Skipped backing up subresource: %s", res.Name)
				continue
			}

			if !canListResource(res.Verbs) {
				if canGetResource(res.Verbs) {
					filteredObjects, err := h.gatherObjectsForNonListResource(ctx, res, gv, resourceSelector)
					if err != nil {
						return err
					}
					h.GVResourceToObjects[currGVResource] = filteredObjects
				} else {
					logrus.Infof("Not collecting objects for resource %v since it does not have list or get verbs", res.Name)
				}
				continue
			}

			filteredObjects, err := h.gatherObjectsForResource(ctx, res, gv, resourceSelector)
			if err != nil {
				return err
			}
			// currGVResource contains GV for resource type, its name and if its namespaced or not,
			// example: gv=v1, name=secrets, namespaced=true; filteredObjects are all the objects matching the resourceSelector
			previouslyGatheredForGVR, ok := h.GVResourceToObjects[currGVResource]
			if ok {
				h.GVResourceToObjects[currGVResource] = append(previouslyGatheredForGVR, filteredObjects...)
			} else {
				h.GVResourceToObjects[currGVResource] = filteredObjects
			}
		}
	}
	return nil
}

func (h *ResourceHandler) gatherResourcesForGroupVersion(filter v1.ResourceSelector) ([]k8sv1.APIResource, error) {
	var resourceList, resourceListFromRegex, resourceListFromNames []k8sv1.APIResource

	groupVersion := filter.APIVersion
	logrus.Infof("Gathering resources for groupVersion: %v", groupVersion)

	// first list all resources for given groupversion using discovery API
	resources, err := h.DiscoveryClient.ServerResourcesForGroupVersion(groupVersion)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logrus.Infof("No resources found for groupVersion %v, skipping it", groupVersion)
			return resourceList, nil
		}
		return resourceList, err
	}
	if filter.KindsRegexp == "" && len(filter.Kinds) == 0 {
		// if no filters for resource kind are given, return entire resource list
		return resources.APIResources, nil
	}

	// "resources" list has all resources under given groupVersion, first filter based on KindsRegexp
	if filter.KindsRegexp != "" {
		if filter.KindsRegexp == "." {
			// "." will match anything except the ones in the list of excludeKinds
			for _, res := range resources.APIResources {
				if isKindExcluded(filter.ExcludeKinds, res) {
					continue
				}
				resourceListFromRegex = append(resourceListFromRegex, res)
			}
			return resourceListFromRegex, nil
		}
		// else filter out resource with regex match
		for _, res := range resources.APIResources {
			if isKindExcluded(filter.ExcludeKinds, res) {
				continue
			}
			kindMatched, err := regexp.MatchString(filter.KindsRegexp, res.Kind)
			if err != nil {
				return resourceList, err
			}
			pluralNameMatched, err := regexp.MatchString(filter.KindsRegexp, res.Name)
			if err != nil {
				return resourceList, err
			}
			if !kindMatched && !pluralNameMatched {
				continue
			}

			logrus.Debugf("resource kind %v, matched regex %v", res.Name, filter.KindsRegexp)
			resourceListFromRegex = append(resourceListFromRegex, res)
		}
	}

	// then add the resources from Kinds field to this list
	if len(filter.Kinds) > 0 {
		resourceListAfterRegexMatch := make(map[string]bool)
		// avoid adding same resource twice by checking what's in resourceListFromRegex
		for _, res := range resourceListFromRegex {
			// adding in both, resource.Name (plural) and resource.Kind (singular camelcase)
			resourceListAfterRegexMatch[res.Name] = true
			resourceListAfterRegexMatch[res.Kind] = true
		}
		resourceTypesToInclude := make(map[string]bool)
		for _, kind := range filter.Kinds {
			resourceTypesToInclude[kind] = true
		}
		for _, res := range resources.APIResources {
			// comparing whatever is specified in the Kinds field with both, resource Name (plural) and Kind (singular)
			if resourceTypesToInclude[res.Name] || resourceTypesToInclude[res.Kind] {
				if !resourceListAfterRegexMatch[res.Name] && !resourceListAfterRegexMatch[res.Kind] {
					logrus.Infof("resource kind %v found in list of resources to include", res.Name)
					resourceListFromNames = append(resourceListFromNames, res)
				}
			}
		}
	}
	// combine both lists, obtained from regex and from iterating over the kinds list
	resourceList = append(resourceListFromRegex, resourceListFromNames...)
	return resourceList, nil
}

func (h *ResourceHandler) gatherObjectsForResource(ctx context.Context, res k8sv1.APIResource, gv schema.GroupVersion, filter v1.ResourceSelector) ([]unstructured.Unstructured, error) {
	var filteredByNamespace, filteredObjects []unstructured.Unstructured
	gvr := gv.WithResource(res.Name)
	var dr dynamic.ResourceInterface
	dr = h.DynamicClient.Resource(gvr)

	// only resources that match name+namespace+label combination will be backed up, so we can filter in any order
	filteredByName, err := h.filterByNameAndLabel(ctx, dr, filter)
	if err != nil {
		return filteredObjects, err
	}

	if res.Namespaced {
		if len(filter.Namespaces) > 0 || filter.NamespaceRegexp != "" {
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

func (h *ResourceHandler) filterByNameAndLabel(ctx context.Context, dr dynamic.ResourceInterface, filter v1.ResourceSelector) ([]unstructured.Unstructured, error) {
	var filteredByName, filteredByResourceNames []unstructured.Unstructured
	var labelSelector string

	if filter.LabelSelectors != nil {
		selector, err := k8sv1.LabelSelectorAsSelector(filter.LabelSelectors)
		if err != nil {
			return filteredByName, err
		}
		labelSelector = selector.String()
		logrus.Debugf("Listing objects using label selector %v", labelSelector)
	}

	resourceObjectsList, err := paginateListResults(ctx, dr, k8sv1.ListOptions{LabelSelector: labelSelector})
	if err != nil {
		return filteredByName, err
	}
	filteredByNameMap := make(map[*unstructured.Unstructured]bool)

	if len(filter.ResourceNames) == 0 && filter.ResourceNameRegexp == "" && filter.ExcludeResourceNameRegexp == "" {
		// no filters for names of the resource, return all objects obtained from the list call
		return resourceObjectsList.Items, nil
	}
	// filter out using ResourceNameRegexp
	if filter.ResourceNameRegexp != "" {
		logrus.Debugf("Using ResourceNameRegexp [%s] to filter resource names", filter.ResourceNameRegexp)
		if filter.ResourceNameRegexp == "." {
			// "." will match everything, so return all resources obtained from the list call
			return resourceObjectsList.Items, nil
		}

		for _, resObj := range resourceObjectsList.Items {
			metadata := resObj.Object["metadata"].(map[string]interface{})
			name := metadata["name"].(string)
			nameMatched, err := regexp.MatchString(filter.ResourceNameRegexp, name)
			if err != nil {
				return filteredByName, err
			}
			if !nameMatched {
				logrus.Debugf("Skipping [%s] because it did not match ResourceNameRegexp [%s]", name, filter.ResourceNameRegexp)
				continue
			}
			filteredByName = append(filteredByName, resObj)
			filteredByNameMap[&resObj] = true
		}
	}

	// filter out using ExcludeResourceNameRegexp
	if filter.ExcludeResourceNameRegexp != "" {
		for _, resObj := range resourceObjectsList.Items {
			metadata := resObj.Object["metadata"].(map[string]interface{})
			name := metadata["name"].(string)
			nameMatched, err := regexp.MatchString(filter.ExcludeResourceNameRegexp, name)
			if err != nil {
				return filteredByName, err
			}
			if nameMatched {
				logrus.Debugf("Skipping %s because it did not match ExcludeResourceNameRegexp %s", name, filter.ExcludeResourceNameRegexp)
				continue
			}
			filteredByName = append(filteredByName, resObj)
			filteredByNameMap[&resObj] = true
		}
	}

	// filter by names as fieldSelector:
	if len(filter.ResourceNames) > 0 {
		logrus.Debugf("Using ResourceNames [%s] to filter resource names", strings.Join(filter.ResourceNames, ","))
		// TODO: POST-preview-2: set resourceVersion later when it becomes clear how to use it
		filteredObjectsList, err := paginateListResults(ctx, dr, k8sv1.ListOptions{LabelSelector: labelSelector})
		if err != nil {
			return filteredByName, err
		}
		allowedNames := make(map[string]bool)
		for _, name := range filter.ResourceNames {
			allowedNames[name] = true
		}
		for _, resObj := range filteredObjectsList.Items {
			metadata := resObj.Object["metadata"].(map[string]interface{})
			name := metadata["name"].(string)
			if allowedNames[name] {
				filteredByResourceNames = append(filteredByResourceNames, resObj)
			}
		}

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
		logrus.Debugf("Using Namespaces %s to filter namespaces", strings.Join(filter.Namespaces, ","))
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
	if filter.NamespaceRegexp != "" {
		logrus.Debugf("Using NamespaceRegexp %s to filter resource names", filter.NamespaceRegexp)
		if filter.NamespaceRegexp == "." {
			// "." will match all namespaces, so return all objects obtained after filtering by name
			return filteredByName, nil
		}
		for _, resObj := range filteredByName {
			metadata := resObj.Object["metadata"].(map[string]interface{})
			ns := metadata["namespace"].(string)
			nsMatched, err := regexp.MatchString(filter.NamespaceRegexp, ns)
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

func paginateListResults(ctx context.Context, dr dynamic.ResourceInterface, listOptions k8sv1.ListOptions) (*unstructured.UnstructuredList, error) {
	var resourceObjectsList *unstructured.UnstructuredList
	listOptions.Limit = ListObjectsLimit
	resourceObjectsListFirst, err := dr.List(ctx, listOptions)
	if err != nil {
		return resourceObjectsList, err
	}
	resourceObjectsList = resourceObjectsListFirst
	continueList := resourceObjectsListFirst.GetContinue()
	for continueList != "" {
		listOptions.Continue = continueList
		resourceObjectsListCurr, err := dr.List(ctx, listOptions)
		if err != nil {
			return resourceObjectsList, err
		}
		continueList = resourceObjectsListCurr.GetContinue()
		resourceObjectsList.Items = append(resourceObjectsList.Items, resourceObjectsListCurr.Items...)
	}
	return resourceObjectsList, nil
}

// NOTE: Rancher types CollectionMethods or ResourceMethods verbs do not translate to verbs on k8sv1.APIResource
// Resources that don't have list verb but do have get verb need to be gathered by GET calls. So the filter for them must
// provide exact names and if needed namespaces. Regexp can't be matched in a GET call
func (h *ResourceHandler) gatherObjectsForNonListResource(ctx context.Context, res k8sv1.APIResource, gv schema.GroupVersion, filter v1.ResourceSelector) ([]unstructured.Unstructured, error) {
	var gatheredObjects []unstructured.Unstructured

	// these objects
	if len(filter.ResourceNames) == 0 {
		logrus.Infof("Cannot get objects for res %v since it doesn't allow list, and no resource names are provided", res.Name)
		return gatheredObjects, nil
	}

	if res.Namespaced && len(filter.Namespaces) == 0 {
		logrus.Infof("Cannot get objects for res %v since it doesn't allow list, and no namespaces are provided", res.Name)
		return gatheredObjects, nil
	}

	gvr := gv.WithResource(res.Name)
	var dr dynamic.ResourceInterface
	dr = h.DynamicClient.Resource(gvr)
	if res.Namespaced {
		for _, ns := range filter.Namespaces {
			dr = h.DynamicClient.Resource(gvr).Namespace(ns)
			for _, name := range filter.ResourceNames {
				obj, err := dr.Get(ctx, name, k8sv1.GetOptions{})
				if err != nil {
					return gatheredObjects, err
				}
				gatheredObjects = append(gatheredObjects, *obj)
			}
		}
		return gatheredObjects, nil
	}

	for _, name := range filter.ResourceNames {
		obj, err := dr.Get(ctx, name, k8sv1.GetOptions{})
		if err != nil {
			return gatheredObjects, err
		}
		gatheredObjects = append(gatheredObjects, *obj)
	}

	return gatheredObjects, nil
}

func (h *ResourceHandler) WriteBackupObjects(backupPath string) error {
	for gvResource, resObjects := range h.GVResourceToObjects {
		for _, resObj := range resObjects {
			metadata := resObj.Object["metadata"].(map[string]interface{})
			// if an object has deletiontimestamp and finalizers, back it up. If there are no finalizers, ignore
			if _, deletionTs := metadata["deletionTimestamp"]; deletionTs {
				// for v1/namespace we need to check spec.finalizers, otherwise check metadata.finalizers
				if resObj.GetKind() != "Namespace" {
					if _, finSet := metadata["finalizers"]; !finSet {
						// no finalizers set, don't backup object
						continue
					}
				} else {
					// ignore error because if there is no finalizers and deletionTimestamp is set, namespace should already be deleted
					fins, ok, _ := unstructured.NestedStringSlice(resObj.Object, "spec", "finalizers")
					if !ok || len(fins) == 0 {
						continue
					}
				}
			}

			objName := metadata["name"].(string)
			objFilename := objName

			// TODO: confirm-test deletionTimestamp needs to be dropped
			for _, field := range []string{"uid", "creationTimestamp", "deletionTimestamp", "selfLink", "resourceVersion", "deletionGracePeriodSeconds"} {
				delete(metadata, field)
			}
			gv := gvResource.GroupVersion
			resourcePath := backupPath + "/" + gvResource.Name + "." + gv.Group + "#" + gv.Version
			if err := createResourceDir(resourcePath); err != nil {
				return err
			}

			gr := schema.ParseGroupResource(gvResource.Name + "." + gv.Group)
			encryptionTransformer := h.TransformerMap[gr]
			additionalAuthenticatedData := objName
			if gvResource.Namespaced {
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

			// TODO: POST-preview-2: collect all objects first and then write??
			err := writeToBackup(resObj.Object, resourcePath, objFilename, encryptionTransformer, additionalAuthenticatedData)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func createResourceDir(path string) error {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		err = os.MkdirAll(path, os.ModePerm)
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

func canListResource(verbs k8sv1.Verbs) bool {
	for _, v := range verbs {
		if v == "list" {
			return true
		}
	}
	return false
}

func canGetResource(verbs k8sv1.Verbs) bool {
	for _, v := range verbs {
		if v == "get" {
			return true
		}
	}
	return false
}

func isKindExcluded(excludes []string, res k8sv1.APIResource) bool {
	for _, exclude := range excludes {
		if exclude == res.Name || exclude == res.Kind {
			return true
		}
	}

	return false
}
