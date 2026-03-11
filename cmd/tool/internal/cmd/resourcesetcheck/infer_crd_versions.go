package resourcesetcheck

import (
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
)

var (
	inferOnce sync.Once
	kindToAV  map[string]string
)

// inferAPIVersionForKind attempts to infer a canonical apiVersion for built-in
// Kubernetes resource Kinds (e.g. Deployment -> apps/v1, ConfigMap -> v1).
//
// If it can't infer (likely a CRD/custom kind), it returns "" and callers should
// keep existing behavior (empty apiVersion matches any selector apiVersion).
func inferAPIVersionForKind(kind string) string {
	inferOnce.Do(func() {
		kindToAV = buildKindToAPIVersionMap()
	})

	for _, c := range kindCandidates(kind) {
		if av, ok := kindToAV[strings.ToLower(c)]; ok {
			return av
		}
	}
	return ""
}

func buildKindToAPIVersionMap() map[string]string {
	out := map[string]string{}

	known := scheme.Scheme.AllKnownTypes() // map[GVK]reflect.Type
	byGroupKind := map[schema.GroupKind][]schema.GroupVersionKind{}

	for gvk := range known {
		// Ignore internal/legacy forms.
		if gvk.Version == "" || gvk.Version == "__internal" {
			continue
		}
		gk := schema.GroupKind{Group: strings.ToLower(gvk.Group), Kind: strings.ToLower(gvk.Kind)}
		byGroupKind[gk] = append(byGroupKind[gk], gvk)
	}

	for gk, gvks := range byGroupKind {
		out[gk.Kind] = preferredAPIVersion(gk.Group, gvks)
	}

	return out
}

// preferredAPIVersion returns the canonical apiVersion string for a set of GVKs sharing
// the same group+kind. It prefers the scheme's prioritized version order, falling back
// to the first external version seen.
func preferredAPIVersion(group string, gvks []schema.GroupVersionKind) string {
	for _, gv := range scheme.Scheme.PrioritizedVersionsForGroup(group) {
		for _, gvk := range gvks {
			if gvk.Group == gv.Group && gvk.Version == gv.Version {
				return gv.String()
			}
		}
	}
	return gvks[0].GroupVersion().String()
}

func kindCandidates(kind string) []string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return nil
	}

	seen := map[string]bool{}
	var out []string

	add := func(s string) {
		if s == "" {
			return
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}

	add(kind)
	add(strings.ToLower(kind))

	// Naive singularization for plural resource inputs (e.g. "deployments").
	lower := strings.ToLower(kind)
	if strings.HasSuffix(lower, "s") && len(lower) > 1 {
		add(lower[:len(lower)-1])
	}

	return out
}
