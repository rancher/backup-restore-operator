package backup

import (
	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/wrangler/v3/pkg/condition"
)

type backupClusterOriginConditionMeta struct {
	backupName                     string
	hasClusterOriginID             bool
	clusterOriginID                string
	hasCurrentOriginCondition      bool
	currentOriginCondition         bool
	canInPlaceRestore              bool
	hasInPlaceRestoreCondition     bool
	currentInPlaceRestoreCondition bool
}

func newBackupClusterOriginConditionMeta(controllerClusterId string, backup *v1.Backup) backupClusterOriginConditionMeta {
	conditionMeta := backupClusterOriginConditionMeta{
		backupName:                     backup.Name,
		hasClusterOriginID:             false,
		hasCurrentOriginCondition:      false,
		currentOriginCondition:         false,
		canInPlaceRestore:              false,
		hasInPlaceRestoreCondition:     false,
		currentInPlaceRestoreCondition: false,
	}

	originAnnotationValue, ok := backup.GetAnnotations()[v1.BackupClusterOriginIndex]
	conditionMeta.hasClusterOriginID = ok && originAnnotationValue != ""
	if conditionMeta.hasClusterOriginID {
		conditionMeta.clusterOriginID = originAnnotationValue
	}

	currentOriginConditionString := condition.Cond(v1.BackupConditionClusterOrigin).GetStatus(backup)
	conditionMeta.hasCurrentOriginCondition = currentOriginConditionString != ""
	if !conditionMeta.hasCurrentOriginCondition {
		conditionMeta.currentOriginCondition = currentOriginConditionString == "True"
	}

	if conditionMeta.hasClusterOriginID {
		conditionMeta.canInPlaceRestore = conditionMeta.clusterOriginID == controllerClusterId
	}

	currentInPlaceRestoreString := condition.Cond(v1.BackupConditionInPlaceRestore).GetStatus(backup)
	conditionMeta.hasInPlaceRestoreCondition = currentInPlaceRestoreString != ""
	if !conditionMeta.hasInPlaceRestoreCondition {
		conditionMeta.currentInPlaceRestoreCondition = currentInPlaceRestoreString == "True"
	}

	return conditionMeta
}

// prepareClusterOriginConditions helps set the cluster origin conditions and reports if anything changed in this part of status.
func (h *handler) prepareClusterOriginConditions(backup *v1.Backup) bool {
	conditionChanged := false
	conditionMeta := newBackupClusterOriginConditionMeta(h.kubeSystemNS, backup)

	// Fist pass we only care to set BackupConditionClusterOrigin based on if the context is there
	if !conditionMeta.hasCurrentOriginCondition || conditionMeta.currentOriginCondition != conditionMeta.hasClusterOriginID {
		conditionChanged = true
		condition.Cond(v1.BackupConditionClusterOrigin).SetStatusBool(backup, conditionMeta.hasClusterOriginID)

		if conditionMeta.hasClusterOriginID {
			condition.Cond(v1.BackupConditionClusterOrigin).Message(backup, "Backup has cluster UID attached.")
		} else {
			condition.Cond(v1.BackupConditionClusterOrigin).Message(backup, "No cluster UID attached to backup.")
		}
	}

	// Second pass, we care about the specifics of the ClusterOrigin to set the InPlaceRestore condition
	if !conditionMeta.hasClusterOriginID {
		// When annotation is missing, we'll mark as unable to determine
		condition.Cond(v1.BackupConditionInPlaceRestore).SetStatusBool(backup, false)
		condition.Cond(v1.BackupConditionInPlaceRestore).Message(backup, "Unable to determine if in-place Restore is viable.")
	}

	if !conditionMeta.hasInPlaceRestoreCondition || conditionMeta.canInPlaceRestore != conditionMeta.currentInPlaceRestoreCondition {
		conditionChanged = true
		condition.Cond(v1.BackupConditionInPlaceRestore).SetStatusBool(backup, conditionMeta.canInPlaceRestore)
		if conditionMeta.canInPlaceRestore {
			condition.Cond(v1.BackupConditionInPlaceRestore).Message(backup, "In-place Restore appears viable.")
		} else {
			condition.Cond(v1.BackupConditionInPlaceRestore).Message(backup, "In-place Restore does not appear viable.")
		}
	}

	// When the annotation is present and not changed
	return conditionChanged
}
