package resourcesets

import (
	"embed"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

//go:embed testdata/*.yaml
var testfolder embed.FS

func grabTestStubs(t *testing.T, filename string) ([]unstructured.Unstructured, error) {
	var resourceObjectsList []unstructured.Unstructured

	// Read YAML file
	content, err := testfolder.ReadFile(filepath.Join("testdata", filename))
	if err != nil {
		return nil, err
	}

	// Split YAML file into individual YAML documents
	decoder := yaml.NewYAMLToJSONDecoder(strings.NewReader(string(content)))
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

func TestFilterByName_EmptyFilter(t *testing.T) {
	// Empty filter should get all results.
	mockFilter := v1.ResourceSelector{}

	result := abstractFilterByNameTest(t, mockFilter, "pets01")

	// Make specific asserts on the results here - verify no false positives and no missed items.
	assert.NotNil(t, result)
	assert.Equal(t, 3, len(result))
}

func TestFilterByName_OnlyHasResourceNameRegexp(t *testing.T) {
	// Should get only names starting with hotdog.
	mockFilter := v1.ResourceSelector{
		ResourceNameRegexp: "^hotdog",
	}
	result := abstractFilterByNameTest(t, mockFilter, "hamburgerStand01")
	// Results are not empty...
	assert.NotNil(t, result)
	// Results has exactly 1 item...
	assert.Equal(t, 3, len(result))
	// verify item name
	assert.Equal(t, "hotdog", result[0].GetName())
	assert.Equal(t, "hotdog-stand", result[0].GetNamespace())
	assert.Equal(t, "hotdog-with-cheese", result[1].GetName())
	assert.Equal(t, "hotdog-stand", result[1].GetNamespace())
	assert.Equal(t, "hotdog", result[2].GetName())
	assert.Equal(t, "hotdog-cart", result[2].GetNamespace())
}

func TestFilterByName_ExcludeRegexWithWildcardInclude(t *testing.T) {
	// Regex should match for all items, then exclude any named "default"
	mockFilter := v1.ResourceSelector{
		NamespaceRegexp:           "^cattle-fleet-|^fleet-", // This is superfluous - we only test names in this test.
		ResourceNameRegexp:        ".",
		ExcludeResourceNameRegexp: "^default$",
	}

	result := abstractFilterByNameTest(t, mockFilter, "serviceAccounts01")

	// Make specific asserts on the results here - verify no false positives and no missed items.
	// Results are not empty...
	assert.NotNil(t, result)
	// Results has exactly 1 item...
	assert.Equal(t, 1, len(result))
	// verify item name
	assert.Equal(t, "fleet-agent", result[0].GetName())
}

func TestFilterByName_ExcludeRegexWithoutInclude(t *testing.T) {
	// Essentially this test should get any item that's not named "default".
	// Basically equivalent to TestFilterByName_ExcludeRegexWithWildcardInclude, but tests different branch of code.
	mockFilter := v1.ResourceSelector{
		NamespaceRegexp:           "^cattle-fleet-|^fleet-", // This is superfluous - we only test names in this test.
		ExcludeResourceNameRegexp: "^default$",
	}

	result := abstractFilterByNameTest(t, mockFilter, "serviceAccounts01")

	// Make specific asserts on the results here - verify no false positives and no missed items.
	assert.NotNil(t, result)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, "fleet-agent", result[0].GetName())
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

	result := abstractFilterByNameTest(t, mockFilter, "service01")

	// Make specific asserts on the results here - verify no false positives and no missed items.
	assert.NotNil(t, result)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, "gitjob", result[0].GetName())
}
func memberOf(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func TestFilterByName_OnlyResourceNames(t *testing.T) {
	// Results should be any items with these 3 exact names.
	resourceNames := []string{"hotdog", "hamburger", "fries"}
	mockFilter := v1.ResourceSelector{
		ResourceNames: resourceNames,
	}

	result := abstractFilterByNameTest(t, mockFilter, "hamburgerStand01")

	// Make specific asserts on the results here - verify no false positives and no missed items.
	assert.NotNil(t, result)
	assert.Equal(t, 3, len(result))
	for _, obj := range result {
		assert.True(t, memberOf(resourceNames, obj.GetName()))
	}
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

	result := abstractFilterByNameTest(t, mockFilter, "hamburgerStand01")

	// Make specific asserts on the results here - verify no false positives and no missed items.
	assert.NotNil(t, result)
	assert.Equal(t, 5, len(result))
	// TODO: Assert that there are not duplicates?
}

func TestFilterByName_ExcludeRegexWithResourceNameMiss(t *testing.T) {
	// This test verifies what happens when `filteredByResourceNames` is equal to 0
	resourceNames := []string{"lobster", "geoduck"}
	mockFilter := v1.ResourceSelector{
		ExcludeResourceNameRegexp: ".*burger.*|^raw-",
		ResourceNames:             resourceNames,
	}

	result := abstractFilterByNameTest(t, mockFilter, "hamburgerStand01")

	// Make specific asserts on the results here - verify no false positives and no missed items.
	assert.NotNil(t, result)
	assert.Equal(t, 4, len(result))
}

// Intentionally failing due to existing bugs
// TODO: Expand filterByNamespace to test more scenarios like previous regex tests...
func TestResourceHandler_filterByName_then_filterByNamespace(t *testing.T) {
	var resourceNames = []string{"hamburger", "cheeseburger", "fries"}
	mockFilter := v1.ResourceSelector{
		NamespaceRegexp:           "^hotdog",
		ResourceNameRegexp:        ".",
		ExcludeResourceNameRegexp: ".*burger.*|^raw-",
		ResourceNames:             resourceNames,
	}

	result := abstractFilterByNameTest(t, mockFilter, "hamburgerStand01")
	assert.NotNil(t, result)
	assert.Equal(t, 6, len(result)) // Only expected items...

	handler := &ResourceHandler{}
	namespaceResult, _ := handler.filterByNamespace(mockFilter, result)
	assert.NotNil(t, namespaceResult)
	assert.Equal(t, 4, len(namespaceResult)) // Should be 5 after bug fix
}
