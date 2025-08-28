package backup

import (
	"regexp"
	"testing"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		Status: v1.BackupStatus{
			BackupType: v1.RecurringBackupType,
		},
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
		Status: v1.BackupStatus{
			BackupType: v1.RecurringBackupType,
		},
		Spec: v1.BackupSpec{
			Schedule: "0 0 * * *",
		},
	}
	backup.SetName(backupName)

	err := mockHandler.validateBackupSpec(backup)

	require.NoError(t, err, "Error when Validating backup spec")
}

func TestValidateBackupSpecOneTime(t *testing.T) {
	backupName := "TestName"

	mockHandler := handler{}

	backup := &v1.Backup{
		Status: v1.BackupStatus{
			BackupType: v1.OneTimeBackupType,
		},
	}
	backup.SetName(backupName)

	err := mockHandler.validateBackupSpec(backup)

	require.NoError(t, err, "Error when Validating backup spec")
	require.Equal(t, backup.Spec.RetentionCount, int64(1))
}

func TestBackupIsSingularAndComplete(t *testing.T) {
	newInput := func(gen, obvgen int64, backupType v1.BackupType) *v1.Backup {
		return &v1.Backup{
			ObjectMeta: metav1.ObjectMeta{
				Generation: gen,
			},
			Status: v1.BackupStatus{
				ObservedGeneration: obvgen,
				BackupType:         backupType,
			},
		}
	}

	testCases := []struct {
		name     string
		input    *v1.Backup
		expected bool
	}{
		{
			name:     "Processed one-time backup",
			input:    newInput(1, 1, v1.OneTimeBackupType),
			expected: true,
		},
		{
			name:     "Unprocessed one-time backup",
			input:    newInput(2, 1, v1.OneTimeBackupType),
			expected: false,
		},
		{
			name:     "Recurring backup",
			input:    newInput(1, 1, v1.RecurringBackupType),
			expected: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			result := backupIsSingularAndComplete(testCase.input)
			assert.Equal(t, testCase.expected, result)
		})
	}
}
