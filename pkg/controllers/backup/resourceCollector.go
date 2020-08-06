package backup

import (
	"fmt"
	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	"github.com/sirupsen/logrus"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage/value"
	"k8s.io/client-go/dynamic"
	"regexp"
	"strings"
)

func (h *handler) gatherResources(filters []v1.BackupFilter, backupPath string, transformerMap map[schema.GroupResource]value.Transformer) error {
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
				continue
			}
			err := h.gatherAndWriteObjectsForResource(res, gv, filter, backupPath, transformerMap)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *handler) gatherResourcesForGroupVersion(filter v1.BackupFilter) ([]k8sv1.APIResource, error) {
	var resourceList, resourceListFromRegex, resourceListFromNames []k8sv1.APIResource
	groupVersion := filter.ApiGroup

	// TODO: accept all groupVersions in one filter
	//if groupVersion == "*" {
	//	_, resources, err = h.discoveryClient.ServerGroupsAndResources()
	//	if err != nil {
	//		return resourceList, err
	//	}
	//}
	resources, err := h.discoveryClient.ServerResourcesForGroupVersion(groupVersion)
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

func (h *handler) gatherAndWriteObjectsForResource(res k8sv1.APIResource, gv schema.GroupVersion, filter v1.BackupFilter, backupPath string, transformerMap map[schema.GroupResource]value.Transformer) error {
	var filteredByNamespace, filteredObjects []unstructured.Unstructured

	gvr := gv.WithResource(res.Name)
	var dr dynamic.ResourceInterface
	dr = h.dynamicClient.Resource(gvr)

	filteredByName, err := h.filterByNameAndLabel(dr, filter)
	if err != nil {
		return err
	}

	if res.Namespaced {
		if len(filter.Namespaces) > 0 || filter.NamespaceRegex != "" {
			filteredByNamespace, err = h.filterByNamespace(filter, filteredByName)
			if err != nil {
				return err
			}
			filteredObjects = filteredByNamespace
			return h.writeBackupObjects(filteredObjects, res, gv, backupPath, transformerMap)
		}
	}
	filteredObjects = filteredByName
	return h.writeBackupObjects(filteredObjects, res, gv, backupPath, transformerMap)
}

func (h *handler) filterByNameAndLabel(dr dynamic.ResourceInterface, filter v1.BackupFilter) ([]unstructured.Unstructured, error) {
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

	resourceObjectsList, err := dr.List(h.ctx, k8sv1.ListOptions{LabelSelector: labelSelector})
	// TODO: get resourceVersion and do another call for List same for all resources; check err for resourceVersion
	if err != nil {
		return filteredByName, err
	}
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
		filteredObjectsList, err := dr.List(h.ctx, k8sv1.ListOptions{FieldSelector: fieldSelector, LabelSelector: labelSelector})
		if err != nil {
			return filteredByName, err
		}
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

func (h *handler) filterByNamespace(filter v1.BackupFilter, filteredByName []unstructured.Unstructured) ([]unstructured.Unstructured, error) {
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
