package restore

import (
	"encoding/json"
	"fmt"

	"github.com/sirupsen/logrus"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// mergeVersionsFromLiveCRD merges any spec.versions from the live cluster CRD that
// are absent from the backup CRD into the backup CRD data in memory, before the
// backup resource is applied via restoreResource.
//
// This handles version rollbacks where a CRD gained a new API version (e.g. v1beta2)
// during an upgrade. Kubernetes enforces that status.storedVersions must remain a
// subset of spec.versions at all times, so a plain replace of the CRD spec with
// the older backup would be rejected if the live cluster has stored objects under
// the newer version.
//
// By keeping the live cluster's extra versions in spec.versions (marked storage:false),
// the invariant is satisfied, objects stored under those versions remain readable,
// and the backup's chosen storage version takes effect for new writes.
func (h *handler) mergeVersionsFromLiveCRD(crdInfo objInfo, backupCRD *unstructured.Unstructured) error {
	dr := h.dynamicClient.Resource(crdInfo.GVR)

	liveCRD, err := dr.Get(h.ctx, crdInfo.Name, k8sv1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// CRD doesn't exist yet — restoreResource will create it fresh, no issue.
			return nil
		}
		return fmt.Errorf("error getting live CRD %v: %w", crdInfo.Name, err)
	}

	// Build a set of version names already present in the backup CRD.
	backupSpecVersions, _, _ := unstructured.NestedSlice(backupCRD.Object, "spec", "versions")
	backupVersionNames := map[string]bool{}
	for _, v := range backupSpecVersions {
		vm, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if name, ok := vm["name"].(string); ok {
			backupVersionNames[name] = true
		}
	}

	// Collect any live spec.versions entries that are absent from the backup.
	liveSpecVersions, _, _ := unstructured.NestedSlice(liveCRD.Object, "spec", "versions")
	var added []string
	for _, v := range liveSpecVersions {
		vm, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := vm["name"].(string)
		if backupVersionNames[name] {
			continue
		}

		// Deep-copy the live version entry via a JSON round-trip so we don't
		// mutate the live CRD object.
		raw, err := json.Marshal(vm)
		if err != nil {
			return fmt.Errorf("error marshaling live version %q for CRD %v: %w", name, crdInfo.Name, err)
		}
		var vCopy map[string]interface{}
		if err := json.Unmarshal(raw, &vCopy); err != nil {
			return fmt.Errorf("error unmarshaling live version %q for CRD %v: %w", name, crdInfo.Name, err)
		}

		// The backup's version retains the storage:true role. Extra live versions
		// are kept only to satisfy the storedVersions invariant and keep objects
		// stored under those versions readable after the rollback.
		vCopy["storage"] = false

		backupSpecVersions = append(backupSpecVersions, vCopy)
		added = append(added, name)
	}

	if len(added) == 0 {
		logrus.Debugf("mergeVersionsFromLiveCRD: CRD %v has no extra live versions to merge", crdInfo.Name)
		return nil
	}

	logrus.Infof("mergeVersionsFromLiveCRD: merging live versions %v into backup CRD %v to satisfy storedVersions invariant", added, crdInfo.Name)
	return unstructured.SetNestedSlice(backupCRD.Object, backupSpecVersions, "spec", "versions")
}
