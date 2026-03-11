package chart

import (
	"fmt"
	"regexp"
	"strings"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8slabels "k8s.io/apimachinery/pkg/labels"
)

// ResourceInfo describes the k8s resource to check against ResourceSet rules.
// Fields left empty are treated as "not provided" and their corresponding selector
// conditions are reported as caveats rather than causing a non-match.
type ResourceInfo struct {
	APIVersion string            // empty means match any apiVersion
	Kind       string            // required
	Name       string            // required
	Namespace  string            // empty means cluster-scoped or not provided
	Labels     map[string]string // empty means label selectors will not be checked
}

// MatchResult describes a ResourceSelector rule that matched the resource.
type MatchResult struct {
	ResourceSetName string
	SelectorIndex   int      // 1-based display index
	SelectorSources []string // chart-relative source files the selector originated from
	Selector        v1.ResourceSelector
	// Caveats lists selector conditions that could not be checked offline
	// (e.g. namespace not provided, label selectors without labels, field selectors).
	Caveats []string
}

// Check tests res against every selector in every ResourceSet and returns ALL rules
// that match — it never short-circuits on the first hit.
//
// This intentionally inverts the operator's runtime flow: the operator uses
// ResourceSet rules as k8s query qualifiers (rules → fetch matching resources).
// Check does the reverse — given a single resource, it finds every rule across
// every ResourceSet that would cover it, so callers get the complete picture of
// which backup rules apply to the resource.
//
// Matching mirrors the operator's offline-checkable logic: apiVersion, kind, name,
// namespace, and label selectors (when labels are available). Conditions that cannot
// be evaluated without a live cluster (missing namespace, missing labels, field
// selectors) are recorded as Caveats on the relevant MatchResult rather than
// causing a non-match.
func Check(res ResourceInfo, resourceSets []*AnnotatedResourceSet) ([]MatchResult, error) {
	var results []MatchResult
	for _, ars := range resourceSets {
		for i, sel := range ars.ResourceSelectors {
			result, matched, err := tryMatch(res, sel, i+1, ars.Name, ars.SelectorSources[i])
			if err != nil {
				return nil, err
			}
			if matched {
				results = append(results, result)
			}
		}
	}
	return results, nil
}

func tryMatch(res ResourceInfo, sel v1.ResourceSelector, idx int, rsName string, sources []string) (MatchResult, bool, error) {
	r := MatchResult{
		ResourceSetName: rsName,
		SelectorIndex:   idx,
		SelectorSources: sources,
		Selector:        sel,
	}

	// apiVersion: if provided by caller, must match exactly.
	if res.APIVersion != "" && res.APIVersion != sel.APIVersion {
		return r, false, nil
	}

	// kind
	kindMatch, err := matchesKind(res.Kind, sel)
	if err != nil {
		return r, false, fmt.Errorf("rule %d (%s): kind: %w", idx, rsName, err)
	}
	if !kindMatch {
		return r, false, nil
	}

	// name
	nameMatch, err := matchesName(res.Name, sel)
	if err != nil {
		return r, false, fmt.Errorf("rule %d (%s): name: %w", idx, rsName, err)
	}
	if !nameMatch {
		return r, false, nil
	}

	// namespace: only checked when the caller provided a namespace AND the selector
	// has namespace constraints. If namespace is absent, note it as a caveat.
	if len(sel.Namespaces) > 0 || sel.NamespaceRegexp != "" {
		if res.Namespace != "" {
			nsMatch, err := matchesNamespace(res.Namespace, sel)
			if err != nil {
				return r, false, fmt.Errorf("rule %d (%s): namespace: %w", idx, rsName, err)
			}
			if !nsMatch {
				return r, false, nil
			}
		} else {
			r.Caveats = append(r.Caveats, "namespace filter not checked (use --namespace to specify one)")
		}
	}

	// label selectors: only checked when labels are available from a resource file.
	if sel.LabelSelectors != nil {
		if len(res.Labels) > 0 {
			lblMatch, err := matchesLabels(res.Labels, sel.LabelSelectors)
			if err != nil {
				return r, false, fmt.Errorf("rule %d (%s): labels: %w", idx, rsName, err)
			}
			if !lblMatch {
				return r, false, nil
			}
		} else {
			r.Caveats = append(r.Caveats, "label selector not checked (use --resource-path to include labels)")
		}
	}

	// field selectors: cannot be evaluated offline.
	if len(sel.FieldSelectors) > 0 {
		r.Caveats = append(r.Caveats, "field selector not checked (requires live cluster)")
	}

	return r, true, nil
}

// matchesKind mirrors collector.filterByKind for a single user-supplied kind string.
// Because the operator matches against both the singular Kind (e.g. "Deployment") and
// the plural resource name (e.g. "deployments"), we try the kind as given, lowercase,
// and a naive lowercase plural so that either form works from user input.
func matchesKind(kind string, sel v1.ResourceSelector) (bool, error) {
	// No kind filter: matches all.
	if len(sel.Kinds) == 0 && sel.KindsRegexp == "" {
		return true, nil
	}

	candidates := kindCandidates(kind)

	// Check the explicit Kinds list first (OR with regexp, mirrors operator).
	for _, c := range candidates {
		for _, k := range sel.Kinds {
			if strings.EqualFold(k, c) {
				return !isKindExcluded(candidates, sel.ExcludeKinds), nil
			}
		}
	}

	if sel.KindsRegexp == "" {
		return false, nil
	}
	// "." is the operator's catch-all kindsRegexp.
	if sel.KindsRegexp == "." {
		return !isKindExcluded(candidates, sel.ExcludeKinds), nil
	}

	re, err := regexp.Compile(sel.KindsRegexp)
	if err != nil {
		return false, fmt.Errorf("compiling kindsRegexp %q: %w", sel.KindsRegexp, err)
	}
	for _, c := range candidates {
		if re.MatchString(c) {
			return !isKindExcluded(candidates, sel.ExcludeKinds), nil
		}
	}
	return false, nil
}

// kindCandidates returns the distinct strings to try when matching a kind:
// the original, lowercase, and a naive lowercase plural (only if not already pluralised).
func kindCandidates(kind string) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	add(kind)
	lower := strings.ToLower(kind)
	add(lower)
	if !strings.HasSuffix(lower, "s") {
		add(lower + "s")
	}
	return out
}

func isKindExcluded(candidates []string, excludeKinds []string) bool {
	for _, c := range candidates {
		for _, ex := range excludeKinds {
			if strings.EqualFold(ex, c) {
				return true
			}
		}
	}
	return false
}

// matchesName mirrors collector.filterByName for a single resource name.
func matchesName(name string, sel v1.ResourceSelector) (bool, error) {
	// No name filter: matches all.
	if len(sel.ResourceNames) == 0 && sel.ResourceNameRegexp == "" && sel.ExcludeResourceNameRegexp == "" {
		return true, nil
	}

	// Exact name match takes priority (OR with regexp, mirrors operator).
	for _, n := range sel.ResourceNames {
		if n == name {
			return true, nil
		}
	}

	// Regexp path (only entered if name wasn't in the exact list).
	hasRegexp := sel.ResourceNameRegexp != "" || sel.ExcludeResourceNameRegexp != ""
	if !hasRegexp {
		return false, nil
	}

	var includeRe, excludeRe *regexp.Regexp
	var err error
	if sel.ResourceNameRegexp != "" {
		includeRe, err = regexp.Compile(sel.ResourceNameRegexp)
		if err != nil {
			return false, fmt.Errorf("compiling resourceNameRegexp %q: %w", sel.ResourceNameRegexp, err)
		}
	}
	if sel.ExcludeResourceNameRegexp != "" {
		excludeRe, err = regexp.Compile(sel.ExcludeResourceNameRegexp)
		if err != nil {
			return false, fmt.Errorf("compiling excludeResourceNameRegexp %q: %w", sel.ExcludeResourceNameRegexp, err)
		}
	}

	// include regex nil means "include everything", mirrors: includeRegex == nil || includeRegex.MatchString(name)
	if includeRe != nil && !includeRe.MatchString(name) {
		return false, nil
	}
	if excludeRe != nil && excludeRe.MatchString(name) {
		return false, nil
	}
	return true, nil
}

// matchesNamespace mirrors collector.filterByNamespace for a single namespace string.
func matchesNamespace(namespace string, sel v1.ResourceSelector) (bool, error) {
	if len(sel.Namespaces) == 0 && sel.NamespaceRegexp == "" {
		return true, nil
	}
	for _, ns := range sel.Namespaces {
		if ns == namespace {
			return true, nil
		}
	}
	if sel.NamespaceRegexp != "" {
		if sel.NamespaceRegexp == "." {
			return true, nil
		}
		re, err := regexp.Compile(sel.NamespaceRegexp)
		if err != nil {
			return false, fmt.Errorf("compiling namespaceRegexp %q: %w", sel.NamespaceRegexp, err)
		}
		if re.MatchString(namespace) {
			return true, nil
		}
	}
	return false, nil
}

// matchesLabels evaluates a LabelSelector against a resource's labels.
func matchesLabels(resLabels map[string]string, labelSel *metav1.LabelSelector) (bool, error) {
	selector, err := metav1.LabelSelectorAsSelector(labelSel)
	if err != nil {
		return false, fmt.Errorf("parsing labelSelector: %w", err)
	}
	return selector.Matches(k8slabels.Set(resLabels)), nil
}
