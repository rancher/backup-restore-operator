package chart

import (
	"encoding/json"
	"fmt"
	"strings"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	helmchart "helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
	"sigs.k8s.io/yaml"
)

// AnnotatedResourceSet wraps a ResourceSet with per-selector source file attribution.
type AnnotatedResourceSet struct {
	*v1.ResourceSet
	// SelectorSources is parallel to ResourceSelectors. Each entry is the chart-relative
	// path of the source file the selector originated from, with the leading "files/" prefix
	// stripped (e.g. "default/basic-resourceset-contents/aks.yaml"), or "" if the source
	// could not be determined.
	SelectorSources []string
}

// LoadAndRenderResourceSets loads the rancher-backup chart from chartPath (a directory or .tgz),
// renders it with all optional resources enabled, and returns all ResourceSet objects with
// per-selector source file attribution derived from the chart's files/ directory.
func LoadAndRenderResourceSets(chartPath string) ([]*AnnotatedResourceSet, error) {
	chrt, err := loader.Load(chartPath)
	if err != nil {
		return nil, fmt.Errorf("loading chart from %q: %w", chartPath, err)
	}

	releaseOpts := chartutil.ReleaseOptions{
		Name:      "rancher-backup",
		Namespace: "cattle-resources-system",
		IsInstall: true,
	}
	// Enable all optional resource groups so every possible selector rule is included.
	vals := withAllOptionalsEnabled(chrt.Values)
	values, err := chartutil.ToRenderValues(chrt, vals, releaseOpts, nil)
	if err != nil {
		return nil, fmt.Errorf("building render values: %w", err)
	}

	rendered, err := engine.Engine{}.Render(chrt, values)
	if err != nil {
		return nil, fmt.Errorf("rendering chart: %w", err)
	}

	resourceSets, err := extractResourceSets(rendered)
	if err != nil {
		return nil, err
	}

	// Build a fingerprint index from the chart's static source files so we can
	// attribute each rendered selector back to the file it came from.
	sourceIndex := buildSourceIndex(chrt.Files)

	return annotate(resourceSets, sourceIndex), nil
}

// annotate pairs each selector in each ResourceSet with its source file from the index.
func annotate(resourceSets []*v1.ResourceSet, sourceIndex map[string]string) []*AnnotatedResourceSet {
	out := make([]*AnnotatedResourceSet, len(resourceSets))
	for i, rs := range resourceSets {
		sources := make([]string, len(rs.ResourceSelectors))
		for j, sel := range rs.ResourceSelectors {
			if fp, err := selectorFingerprint(sel); err == nil {
				sources[j] = sourceIndex[fp]
			}
		}
		out[i] = &AnnotatedResourceSet{ResourceSet: rs, SelectorSources: sources}
	}
	return out
}

// buildSourceIndex parses all files/**/*.yaml entries from the chart's Files collection,
// interprets each as a list of ResourceSelectors, and returns a map of
// fingerprint → chart-relative path with the "files/" prefix stripped
// (e.g. "default/basic-resourceset-contents/aks.yaml").
func buildSourceIndex(files []*helmchart.File) map[string]string {
	index := make(map[string]string)
	for _, f := range files {
		if !strings.HasPrefix(f.Name, "files/") || !strings.HasSuffix(f.Name, ".yaml") || !strings.HasSuffix(f.Name, ".yml") {
			continue
		}
		var selectors []v1.ResourceSelector
		if err := yaml.Unmarshal(f.Data, &selectors); err != nil || len(selectors) == 0 {
			continue
		}
		base := stripFilesPrefix(f.Name)
		for _, sel := range selectors {
			if fp, err := selectorFingerprint(sel); err == nil {
				index[fp] = base
			}
		}
	}
	return index
}

func stripFilesPrefix(filePath string) string {
	return strings.TrimPrefix(filePath, "files/")
}

// selectorFingerprint returns a canonical JSON string for sel, used as a map key.
// encoding/json sorts map keys, so the output is deterministic for the same selector.
func selectorFingerprint(sel v1.ResourceSelector) (string, error) {
	data, err := json.Marshal(sel)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// withAllOptionalsEnabled returns a shallow copy of vals with every entry under
// optionalResources.<key>.enabled set to true. This ensures that every possible
// ResourceSelector rule is included in the render output, regardless of the chart defaults.
func withAllOptionalsEnabled(vals map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(vals))
	for k, v := range vals {
		result[k] = v
	}

	optRes, ok := vals["optionalResources"].(map[string]interface{})
	if !ok || len(optRes) == 0 {
		return result
	}

	enabled := make(map[string]interface{}, len(optRes))
	for key, val := range optRes {
		if m, ok := val.(map[string]interface{}); ok {
			entry := make(map[string]interface{}, len(m))
			for k, v := range m {
				entry[k] = v
			}
			entry["enabled"] = true
			enabled[key] = entry
		} else {
			enabled[key] = val
		}
	}
	result["optionalResources"] = enabled
	return result
}

// extractResourceSets parses rendered helm template output and returns any ResourceSet objects found.
func extractResourceSets(rendered map[string]string) ([]*v1.ResourceSet, error) {
	var out []*v1.ResourceSet

	for key, content := range rendered {
		if !strings.Contains(key, "resourceset") {
			continue
		}

		// Quick kind check before full unmarshal.
		var meta struct {
			Kind string `json:"kind"`
		}
		if err := yaml.Unmarshal([]byte(content), &meta); err != nil || meta.Kind != "ResourceSet" {
			continue
		}

		var rs v1.ResourceSet
		if err := yaml.Unmarshal([]byte(content), &rs); err != nil {
			return nil, fmt.Errorf("parsing ResourceSet: %w", err)
		}
		out = append(out, &rs)
	}

	return out, nil
}
