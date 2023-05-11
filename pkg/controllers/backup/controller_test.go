package backup

import (
	"regexp"
	"testing"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateBackupFilename(t *testing.T) {
	// Expected output: TestName-TestNamespace-2023-05-08T13-40-33-04-00
	// The timestamp is based on time.Now() so we don't check for it explicitly
	namespace := "TestNamespace"
	backup_name := "TestName"

	mock_handler := handler{
		kubeSystemNS: namespace,
	}

	backup := &v1.Backup{}
	backup.SetName(backup_name)

	filename, err := mock_handler.generateBackupFilename(backup)

	require.NoError(t, err, "Error when generating backup filename")

	wantNamespace := regexp.MustCompile(`\b` + namespace + `\b`)
	wantName := regexp.MustCompile(`\b` + backup_name + `\b`)

	assert.True(t, wantNamespace.MatchString(filename), "Expected namespace in generated filename")
	assert.True(t, wantName.MatchString(filename), "Expected backup name in generated filename")
}

func TestValidateBackupSpecFail(t *testing.T) {
	// Expected output: TestName-TestNamespace-2023-05-08T13-40-33-04-00
	// The timestamp is based on time.Now() so we don't check for it explicitly
	backup_name := "TestName"

	mock_handler := handler{}

	backup := &v1.Backup{
		Spec: v1.BackupSpec{
			Schedule: "kldsnd",
		},
	}
	backup.SetName(backup_name)

	err := mock_handler.validateBackupSpec(backup)

	require.Error(t, err, "Error when Validating backup spec")
}

func TestValidateBackupSpecPass(t *testing.T) {
	// Expected output: TestName-TestNamespace-2023-05-08T13-40-33-04-00
	// The timestamp is based on time.Now() so we don't check for it explicitly
	backup_name := "TestName"

	mock_handler := handler{}

	backup := &v1.Backup{
		Spec: v1.BackupSpec{
			Schedule: "0 0 * * *",
		},
	}
	backup.SetName(backup_name)

	err := mock_handler.validateBackupSpec(backup)

	require.NoError(t, err, "Error when Validating backup spec")
}
