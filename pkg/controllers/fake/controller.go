package fake

import (
	"context"
	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	backupControllers "github.com/mrajashree/backup/pkg/generated/controllers/backupper.cattle.io/v1"
)

type handler struct {
	ctx            context.Context
	fakeStatusDefs backupControllers.FakeTestController
}

func Register(
	ctx context.Context,
	fakeStatusDefs backupControllers.FakeTestController) {
	controller := handler{
		ctx:            ctx,
		fakeStatusDefs: fakeStatusDefs,
	}

	fakeStatusDefs.OnChange(ctx, "backups", controller.OnBackupChange)
}

func (h *handler) OnBackupChange(_ string, fakeStatusDef *v1.FakeTest) (*v1.FakeTest, error) {
	//fmt.Printf("\nfakeStatusDef: %v\n", fakeStatusDef.Spec.ValInt)
	//if fakeStatusDef.Status.Generation == 900 {
	//	return fakeStatusDef, nil
	//}
	//fakeStatusDef.Status.Generation = 900
	//_, err := h.fakeStatusDefs.UpdateStatus(fakeStatusDef)
	//if err != nil {
	//	fmt.Printf("\nerror updating obj: %v\n", err)
	//	return fakeStatusDef, err
	//}
	return fakeStatusDef, nil
}
