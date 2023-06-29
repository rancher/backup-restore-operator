package backup

import (
	"regexp"
	"testing"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateBackupFilename(t *testing.T) {
	namespace := "TestNamespace"
	backupName := "TestName"

	mockHandler := handler{
		kubeSystemNS: namespace,
	}

	backup := &v1.Backup{}
	backup.SetName(backupName)

	filename, err := mockHandler.generateBackupFilename(backup)

	require.NoError(t, err, "Error when generating backup filename")

	wantNamespace := regexp.MustCompile(`\b` + namespace + `\b`)
	wantName := regexp.MustCompile(`\b` + backupName + `\b`)

	assert.True(t, wantNamespace.MatchString(filename), "Expected namespace in generated filename")
	assert.True(t, wantName.MatchString(filename), "Expected backup name in generated filename")
}

func TestValidateBackupSpecFail(t *testing.T) {
	backupName := "TestName"

	mockHandler := handler{}

	backup := &v1.Backup{
		Spec: v1.BackupSpec{
			Schedule: "kldsnd",
		},
	}
	backup.SetName(backupName)

	err := mockHandler.validateBackupSpec(backup)

	require.Error(t, err, "Error when Validating backup spec")
}

func TestValidateBackupSpecPass(t *testing.T) {
	// Expected output: TestName-TestNamespace-2023-05-08T13-40-33-04-00
	// The timestamp is based on time.Now() so we don't check for it explicitly
	backupName := "TestName"

	mockHandler := handler{}

	backup := &v1.Backup{
		Spec: v1.BackupSpec{
			Schedule: "0 0 * * *",
		},
	}
	backup.SetName(backupName)

	err := mockHandler.validateBackupSpec(backup)

	require.NoError(t, err, "Error when Validating backup spec")
}
