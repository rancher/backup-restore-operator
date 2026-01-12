package resourcesets

import (
	"bytes"
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
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sEncryptionconfig "k8s.io/apiserver/pkg/server/options/encryptionconfig"
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
	TransformerMap      k8sEncryptionconfig.StaticTransformers
	GVResourceToObjects map[GVResource][]unstructured.Unstructured
	Ctx                 context.Context
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
				logrus.WithFields(logrus.Fields{"name": res.Name}).Debug("Skipped backup of subresource due to configuration settings")
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
					logrus.WithFields(logrus.Fields{"name": res.Name}).Info("Skipping resource collection as list and get verbs are not available")
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
	var resourceList []k8sv1.APIResource

	groupVersion := filter.APIVersion
	logrus.WithFields(logrus.Fields{"group_version": groupVersion}).Debug("Gathering resources for unknown group version")

	// first list all resources for given groupversion using discovery API
	resources, err := h.DiscoveryClient.ServerResourcesForGroupVersion(groupVersion)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logrus.WithFields(logrus.Fields{"group_version": groupVersion}).Debug("No resources found for group version, skipping discovery")
			return resourceList, nil
		}
		return resourceList, err
	}
	return h.filterByKind(filter, resources.APIResources)
}

func (h *ResourceHandler) gatherObjectsForResource(ctx context.Context, res k8sv1.APIResource, gv schema.GroupVersion, filter v1.ResourceSelector) ([]unstructured.Unstructured, error) {
	gvr := gv.WithResource(res.Name)
	dr := h.DynamicClient.Resource(gvr)

	// only resources that match name+namespace and label/field selector combination will be backed up, so we can filter in any order
	// however, in practice filtering by label happens at an API level when we paginate the resources (creating our initial list)
	resourcesFromAPIServer, err := h.fetchResourcesFromAPIServer(ctx, dr, filter)
	if err != nil {
		return nil, err
	}

	filteredByName, err := h.filterByName(filter, resourcesFromAPIServer.Items)
	if err != nil {
		return nil, err
	}

	if res.Namespaced && (len(filter.Namespaces) > 0 || filter.NamespaceRegexp != "") {
		return h.filterByNamespace(filter, filteredByName)
	}
	return filteredByName, nil
}

// fetchResourcesFromAPIServer uses a label selector and/or field selector from the ResourceSelector to fetch, unroll, and return the initial list of objects
func (h *ResourceHandler) fetchResourcesFromAPIServer(ctx context.Context, dr dynamic.ResourceInterface, filter v1.ResourceSelector) (*unstructured.UnstructuredList, error) {
	var labelSelector string
	var fieldSelector string

	if filter.LabelSelectors != nil {
		selector, err := k8sv1.LabelSelectorAsSelector(filter.LabelSelectors)
		if err != nil {
			return nil, err
		}
		labelSelector = selector.String()
		logrus.WithFields(logrus.Fields{"label_selector": labelSelector}).Debug("Listing objects with specified label selector")
	}

	if filter.FieldSelectors != nil {
		fieldSelector = fields.SelectorFromSet(filter.FieldSelectors).String()
		logrus.WithFields(logrus.Fields{"field_selector": fieldSelector}).Debug("Listing objects with field selector filter applied")
	}

	return unrollPaginatedListResult(ctx, dr, k8sv1.ListOptions{LabelSelector: labelSelector, FieldSelector: fieldSelector})
}

func (h *ResourceHandler) filterByKind(filter v1.ResourceSelector, apiResources []k8sv1.APIResource) ([]k8sv1.APIResource, error) {
	var resourceList []k8sv1.APIResource
	var kindRegexp *regexp.Regexp
	if filter.KindsRegexp == "" && len(filter.Kinds) == 0 {
		// if no filters for resource kind are given, return entire resource list
		return apiResources, nil
	}
	if filter.KindsRegexp != "" {
		var err error
		kindRegexp, err = regexp.Compile(filter.KindsRegexp)
		if err != nil {
			return nil, fmt.Errorf("error in kindsRegexp pattern %s: %w", filter.KindsRegexp, err)
		}
	}
	allowedKinds := make(map[string]bool)
	disallowedKinds := make(map[string]bool)
	for _, name := range filter.Kinds {
		allowedKinds[name] = true
	}
	for _, name := range filter.ExcludeKinds {
		disallowedKinds[name] = true
	}

	// "resources" list has all resources under given groupVersion, first filter based on KindsRegexp
	// Look for a match in either `Kind` (singular name) or `Name` (plural name).
	// If we match by regexp, we need to consider exclusions by both `Kind` and `Name`
	// This means we can regexp-match on `Name` but will exclude due to matching on `Kind`, for example
	for _, resObj := range apiResources {
		if allowedKinds[resObj.Kind] || allowedKinds[resObj.Name] {
			resourceList = append(resourceList, resObj)
			continue
		}
		if kindRegexp == nil {
			continue
		}
		if (filter.KindsRegexp == "." || kindRegexp.MatchString(resObj.Kind) || kindRegexp.MatchString(resObj.Name)) &&
			!disallowedKinds[resObj.Kind] && !disallowedKinds[resObj.Name] {
			resourceList = append(resourceList, resObj)
		}
	}
	return resourceList, nil
}

func (h *ResourceHandler) filterByName(filter v1.ResourceSelector, resourceObjectsList []unstructured.Unstructured) ([]unstructured.Unstructured, error) {
	var filteredByName []unstructured.Unstructured

	if len(filter.ResourceNames) == 0 && filter.ResourceNameRegexp == "" && filter.ExcludeResourceNameRegexp == "" {
		// no filters for names of the resource, return all objects obtained from the list call
		return resourceObjectsList, nil
	}

	// Map to store included names for fast lookup
	includeNameMap := make(map[string]bool)
	for _, name := range filter.ResourceNames {
		includeNameMap[name] = true
	}

	var includeRegex *regexp.Regexp
	var err error
	hasRegEx := false
	if filter.ResourceNameRegexp != "" {
		includeRegex, err = regexp.Compile(filter.ResourceNameRegexp)
		if err != nil {
			return nil, fmt.Errorf("error in resource-name pattern %s: %w", filter.ResourceNameRegexp, err)
		}
		hasRegEx = true
	}
	var excludeRegex *regexp.Regexp
	if filter.ExcludeResourceNameRegexp != "" {
		excludeRegex, err = regexp.Compile(filter.ExcludeResourceNameRegexp)
		if err != nil {
			return nil, fmt.Errorf("error in exclude-resource-name pattern %s: %w", filter.ResourceNameRegexp, err)
		}
		hasRegEx = true
	}

	for _, resObj := range resourceObjectsList {
		// Check if the name is in the includeNames list
		name := resObj.GetName()
		if includeNameMap[name] {
			filteredByName = append(filteredByName, resObj)
			logrus.WithFields(logrus.Fields{"name": name}).Debug("Including resource because name matched entry in ResourceNames list")
		} else if hasRegEx {
			includeMatch := includeRegex == nil || includeRegex.MatchString(name)
			if includeMatch {
				excludeMatch := excludeRegex != nil && excludeRegex.MatchString(name)
				if !excludeMatch {
					filteredByName = append(filteredByName, resObj)
					logrus.WithFields(logrus.Fields{"name": name, "resource_name_regexp": filter.ResourceNameRegexp, "exclude_resource_name_regexp": filter.ExcludeResourceNameRegexp}).Debug("Resource included based on name pattern matching rules")
				}
			}
		}
	}

	return filteredByName, nil
}

func (h *ResourceHandler) filterByNamespace(filter v1.ResourceSelector, filteredByName []unstructured.Unstructured) ([]unstructured.Unstructured, error) {
	var filteredObjects []unstructured.Unstructured
	var namespaceRegexp *regexp.Regexp

	if len(filter.Namespaces) == 0 && filter.NamespaceRegexp == "" {
		return filteredByName, nil
	}
	if filter.NamespaceRegexp != "" {
		var err error
		logrus.WithFields(logrus.Fields{"namespace_regexp": filter.NamespaceRegexp}).Debug("Filtering resource names using namespace regular expression")
		namespaceRegexp, err = regexp.Compile(filter.NamespaceRegexp)
		if err != nil {
			return nil, fmt.Errorf("error in namespace pattern %s: %w", filter.NamespaceRegexp, err)
		}
	}
	allowedNamespaces := make(map[string]bool)
	for _, ns := range filter.Namespaces {
		allowedNamespaces[ns] = true
	}
	for _, resObj := range filteredByName {
		namespace := resObj.GetNamespace()
		if allowedNamespaces[namespace] ||
			(filter.NamespaceRegexp != "" && (filter.NamespaceRegexp == "." || namespaceRegexp.MatchString(namespace))) {
			filteredObjects = append(filteredObjects, resObj)
		}
	}
	return filteredObjects, nil
}

func unrollPaginatedListResult(ctx context.Context, dr dynamic.ResourceInterface, listOptions k8sv1.ListOptions) (*unstructured.UnstructuredList, error) {
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
		logrus.WithFields(logrus.Fields{"name": res.Name}).Info("Unable to retrieve objects as resource does not support listing and no specific resource names were provided")
		return gatheredObjects, nil
	}

	if res.Namespaced && len(filter.Namespaces) == 0 {
		logrus.WithFields(logrus.Fields{"name": res.Name}).Info("Unable to retrieve objects for resource: listing not supported and no namespaces specified")
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
					return nil, err
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
			encryptionTransformer := h.TransformerMap.TransformerForResource(gr)
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
			err := writeToBackup(h.Ctx, resObj.Object, resourcePath, objFilename, encryptionTransformer, additionalAuthenticatedData)
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

func writeToBackup(ctx context.Context, resource map[string]interface{}, backupPath, filename string, transformer value.Transformer, additionalAuthenticatedData string) error {
	f, err := os.Create(filepath.Join(backupPath, filepath.Base(filename+".json")))
	if err != nil {
		return fmt.Errorf("error creating temp file: %v", err)
	}
	defer f.Close()

	resourceBytes, err := json.Marshal(resource)
	if err != nil {
		return fmt.Errorf("error converting resource to JSON: %v", err)
	}

	// Since k8s 1.32 we cannot verify a transformer must be run, so it's always run now.
	maybeEncrypted, err := transformer.TransformToStorage(ctx, resourceBytes, value.DefaultContext(additionalAuthenticatedData))
	if err != nil {
		return fmt.Errorf("error converting resource to JSON: %v", err)
	}

	// Verify encrypted bytes are different than resourceBytes
	if !bytes.Equal(resourceBytes, maybeEncrypted) {
		resourceBytes, err = json.Marshal(maybeEncrypted)
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
