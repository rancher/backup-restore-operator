package resourcesets

import (
	"os"
	"runtime"
	"strings"
	"testing"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/stretchr/testify/assert"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func abstractFilterByKindTest(t *testing.T, filter v1.ResourceSelector, stubPath string) []k8sv1.APIResource {
	// Construct resourceObjectsList from a folder of '.yaml' files
	resourceObjectsList, err := grabTestStubs(t, stubPath+".yaml")
	if err != nil {
		t.Fatal(err)
	}

	// Create an instance of ResourceHandler
	handler := &ResourceHandler{}
	// Convert to k8sv1.APIResource
	apiResources := make([]k8sv1.APIResource, len(resourceObjectsList))
	for i, x := range resourceObjectsList {
		apiResources[i] = k8sv1.APIResource{Name: x.GetName(), Kind: x.GetKind()}
	}
	result, err := handler.filterByKind(filter, apiResources)
	assert.NoError(t, err)

	return result
}

func getObjectPropertyFromMetadataByKey(object unstructured.Unstructured, key string) string {
	metadata := object.Object["metadata"].(map[string]interface{})
	value := metadata[key].(string)
	return value
}

func TestFilterByKind_CheckDuplicates(t *testing.T) {
	// Verify that matching by both KindRegexp and Kinds doesn't create duplicates
	var kindNames []string
	kindNames = append(kindNames, "Movie")
	mockFilter := v1.ResourceSelector{
		KindsRegexp: "^[MC]o",
		Kinds:       kindNames,
	}

	result := abstractFilterByKindTest(t, mockFilter, getCurrentFuncName())

	// Make specific asserts on the results here - verify no false positives and no missed items.
	assert.NotNil(t, result)
	assert.Equal(t, 5, len(result))
	for _, obj := range result {
		assert.NotEqual(t, "Music", obj.Kind)
	}

	// Same data, but with exclusion only. Verify that everything is matched
	mockFilter = v1.ResourceSelector{
		KindsRegexp:  "",
		Kinds:        []string{},
		ExcludeKinds: []string{"Coffee"}, // This isn't hit, because the other kind-filters are empty
	}
	result = abstractFilterByKindTest(t, mockFilter, getCurrentFuncName())
	assert.NotNil(t, result)
	assert.Equal(t, 9, len(result))

	// Same data, verify a kind-thing works
	mockFilter = v1.ResourceSelector{
		KindsRegexp:  "",
		Kinds:        []string{"Coffee"},
		ExcludeKinds: []string{"Shovels"},
	}
	result = abstractFilterByKindTest(t, mockFilter, getCurrentFuncName())
	assert.NotNil(t, result)
	assert.Equal(t, 2, len(result))
	for _, obj := range result {
		assert.Equal(t, "Coffee", obj.Kind)
	}
}

func TestFilterByName_EmptyFilter(t *testing.T) {
	// Empty filter should get all results.
	mockFilter := v1.ResourceSelector{}

	result := abstractFilterByNameTest(t, mockFilter, getCurrentFuncName())

	// Make specific asserts on the results here - verify no false positives and no missed items.
	assert.NotNil(t, result)
	assert.Equal(t, 3, len(result))
}

func TestFilterByName_OnlyHasResourceNameRegexp(t *testing.T) {
	// Should get only names starting with hotdog.
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
	// Regex should match for all items, then exclude any named "default"
	mockFilter := v1.ResourceSelector{
		NamespaceRegexp:           "^cattle-fleet-|^fleet-", // This is superfluous - we only test names in this test.
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
	// Essentially this test should get any item that's not named "default".
	// Basically equivalent to TestFilterByName_ExcludeRegexWithWildcardInclude, but tests different branch of code.
	mockFilter := v1.ResourceSelector{
		NamespaceRegexp:           "^cattle-fleet-|^fleet-", // This is superfluous - we only test names in this test.
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

		Results should be anything matching regex, and listed names.
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
	// Results should be any items with these 3 exact names.
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
	// Results should include specific named items, PLUS any that do not match exclude regex.
	// Most notably, this should not produce results that have duplicates in the list.
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
	// This test verifies what happens when `filteredByResourceNames` is equal to 0
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
