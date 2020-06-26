package restore

import (
	"context"
	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	backupControllers "github.com/mrajashree/backup/pkg/generated/controllers/backupper.cattle.io/v1"
	k8sv1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type handler struct {
	restores backupControllers.RestoreController
	backups  backupControllers.BackupController
}

func Register(
	ctx context.Context,
	restores backupControllers.RestoreController,
	backups backupControllers.BackupController) {

	controller := &handler{
		restores: restores,
		backups:  backups,
	}

	// Register handlers
	restores.OnChange(ctx, "restore", controller.OnRestoreChange)
	//backups.OnRemove(ctx, controllerRemoveName, controller.OnEksConfigRemoved)
}

func (h *handler) OnRestoreChange(_ string, restore *v1.Restore) (*v1.Restore, error) {
	backupName := restore.Spec.BackupName
	h.backups.Get("", backupName, k8sv1.GetOptions{})

	return restore, nil
}
