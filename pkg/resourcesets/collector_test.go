package resourcesets

import (
	"os"
	"runtime"
	"strings"
	"testing"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func getCurrentFuncName() string {
	pc, _, _, _ := runtime.Caller(1)
	fullFuncName := runtime.FuncForPC(pc).Name()
	// Extract just the function name
	lastDotIndex := strings.LastIndex(fullFuncName, ".")
	if lastDotIndex != -1 {
		return fullFuncName[lastDotIndex+1:]
	}
	return fullFuncName
}

func grabTestStubs(t *testing.T, filename string) ([]unstructured.Unstructured, error) {
	var resourceObjectsList []unstructured.Unstructured

	// Read YAML file
	yamlFile, err := os.ReadFile("./../../tests/stubs/" + filename)
	if err != nil {
		return nil, err
	}

	// Split YAML file into individual YAML documents
	decoder := yaml.NewYAMLToJSONDecoder(strings.NewReader(string(yamlFile)))
	for {
		var obj unstructured.Unstructured
		err := decoder.Decode(&obj)
		if err != nil {
			break // End of YAML stream
		}

		resourceObjectsList = append(resourceObjectsList, obj)
	}

	assert.NotNil(t, resourceObjectsList)
	return resourceObjectsList, nil
}

func abstractFilterByNameTest(t *testing.T, filter v1.ResourceSelector, stubPath string) []unstructured.Unstructured {
	// Construct resourceObjectsList from a folder of '.yaml' files
	resourceObjectsList, err := grabTestStubs(t, stubPath+".yaml")
	if err != nil {
		t.Fatal(err)
	}

	// Create an instance of ResourceHandler
	handler := &ResourceHandler{}
	result, err := handler.filterByName(filter, resourceObjectsList)
	assert.NoError(t, err)

	return result
}

func getObjectPropertyFromMetadataByKey(object unstructured.Unstructured, key string) string {
	metadata := object.Object["metadata"].(map[string]interface{})
	value := metadata[key].(string)
	return value
}

func TestFilterByName_EmptyFilter(t *testing.T) {
	mockFilter := v1.ResourceSelector{}

	result := abstractFilterByNameTest(t, mockFilter, getCurrentFuncName())

	// Make specific asserts on the results here - verify no false positives and no missed items.
	assert.NotNil(t, result)
	assert.Equal(t, 3, len(result))
}

func TestFilterByName_OnlyHasResourceNameRegexp(t *testing.T) {
	mockFilter := v1.ResourceSelector{
		ResourceNameRegexp: "^hotdog",
	}
	result := abstractFilterByNameTest(t, mockFilter, getCurrentFuncName())
	// Results are not empty...
	assert.NotNil(t, result)
	// Results has exactly 1 item...
	assert.Equal(t, 2, len(result))
	// verify item name
	assert.Equal(t, "hotdog", getObjectPropertyFromMetadataByKey(result[0], "name"))
	assert.Equal(t, "hotdog-cart", getObjectPropertyFromMetadataByKey(result[0], "namespace"))
}

func TestFilterByName_ExcludeRegexWithWildcardInclude(t *testing.T) {
	mockFilter := v1.ResourceSelector{
		NamespaceRegexp:           "^cattle-fleet-|^fleet-",
		ResourceNameRegexp:        ".",
		ExcludeResourceNameRegexp: "^default$",
	}

	result := abstractFilterByNameTest(t, mockFilter, getCurrentFuncName())

	// Make specific asserts on the results here - verify no false positives and no missed items.
	// Results are not empty...
	assert.NotNil(t, result)
	// Results has exactly 1 item...
	assert.Equal(t, 1, len(result))
	// verify item name
	assert.Equal(t, "fleet-agent", getObjectPropertyFromMetadataByKey(result[0], "name"))
}

func TestFilterByName_ExcludeRegexWithoutInclude(t *testing.T) {
	mockFilter := v1.ResourceSelector{
		NamespaceRegexp:           "^cattle-fleet-|^fleet-",
		ExcludeResourceNameRegexp: "^default$",
	}

	result := abstractFilterByNameTest(t, mockFilter, getCurrentFuncName())

	// Make specific asserts on the results here - verify no false positives and no missed items.
	assert.NotNil(t, result)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, "mustard-bottle", getObjectPropertyFromMetadataByKey(result[0], "name"))
}

func TestFilterByName_ResourceNamesRegexWithStaticNames(t *testing.T) {
	/**
	- resourceNameRegexp: "^fleet-"
	  resourceNames:
	    - "gitjob"
	    - "test"
	*/
	var resourceNames []string
	resourceNames = append(resourceNames, "gitjob")
	resourceNames = append(resourceNames, "test")
	// Filter rule taken from: https://github.com/rancher/backup-restore-operator/blob/b1462f1e20182fd7bfc0d8c57059080a4ab84902/charts/rancher-backup/files/default-resourceset-contents/fleet.yaml#L43C1-L48C15
	mockFilter := v1.ResourceSelector{
		ResourceNameRegexp: "^fleet-",
		ResourceNames:      resourceNames,
	}

	result := abstractFilterByNameTest(t, mockFilter, getCurrentFuncName())

	// Make specific asserts on the results here - verify no false positives and no missed items.
	assert.NotNil(t, result)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, "gitjob", getObjectPropertyFromMetadataByKey(result[0], "name"))
}

func TestFilterByName_OnlyResourceNames(t *testing.T) {
	var resourceNames []string
	resourceNames = append(resourceNames, "hotdog")
	resourceNames = append(resourceNames, "hamburger")
	resourceNames = append(resourceNames, "fries")
	mockFilter := v1.ResourceSelector{
		ResourceNames: resourceNames,
	}

	result := abstractFilterByNameTest(t, mockFilter, getCurrentFuncName())

	// Make specific asserts on the results here - verify no false positives and no missed items.
	assert.NotNil(t, result)
	assert.Equal(t, 2, len(result))
}

// Intentionally failing due to existing bugs
func TestFilterByName_CheckDuplicates(t *testing.T) {
	var resourceNames []string
	resourceNames = append(resourceNames, "hotdog")                // Test has 2
	resourceNames = append(resourceNames, "hamburger-with-cheese") // Test has 1
	mockFilter := v1.ResourceSelector{
		ExcludeResourceNameRegexp: ".*burger.*|^raw-", // Will only include 4 items
		ResourceNames:             resourceNames,
	}
	// Hotdog items should not be duplicated...
	// hamburger-with-cheese will be re-added after it was excluded (making 5 total)

	result := abstractFilterByNameTest(t, mockFilter, getCurrentFuncName())

	// Make specific asserts on the results here - verify no false positives and no missed items.
	assert.NotNil(t, result)
	assert.Equal(t, 5, len(result))
	// TODO: Assert that there are not duplicates?
}

func TestFilterByName_ExcludeRegexWithResourceNameMiss(t *testing.T) {
	var resourceNames []string
	resourceNames = append(resourceNames, "cheeseburger")
	mockFilter := v1.ResourceSelector{
		ExcludeResourceNameRegexp: ".*burger.*|^raw-",
		ResourceNames:             resourceNames,
	}

	result := abstractFilterByNameTest(t, mockFilter, getCurrentFuncName())

	// Make specific asserts on the results here - verify no false positives and no missed items.
	assert.NotNil(t, result)
	assert.Equal(t, 2, len(result))
}

// Intentionally failing due to existing bugs
// TODO: Expand filterByNamespace to test more scenarios like previous regex tests...
func TestResourceHandler_filterByName_then_filterByNamespace(t *testing.T) {
	var resourceNames []string
	resourceNames = append(resourceNames, "hamburger")
	resourceNames = append(resourceNames, "cheeseburger")
	resourceNames = append(resourceNames, "fries")
	mockFilter := v1.ResourceSelector{
		NamespaceRegexp:           "^hotdog",
		ResourceNameRegexp:        ".",
		ExcludeResourceNameRegexp: ".*burger.*|^raw-",
		ResourceNames:             resourceNames,
	}

	result := abstractFilterByNameTest(t, mockFilter, getCurrentFuncName())
	assert.NotNil(t, result)
	assert.NotEqual(t, 9, len(result)) // Not all the stub/mock items
	assert.Equal(t, 7, len(result))    // Only expected items...

	handler := &ResourceHandler{}
	namespaceResult, _ := handler.filterByNamespace(mockFilter, result)
	assert.NotNil(t, namespaceResult)
	// Disabled for now to let tests pass.
	assert.Equal(t, 5, len(namespaceResult)) // Should be 5 after bug fix
}
