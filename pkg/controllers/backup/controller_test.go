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

func TestSetBackupTypeRecurring(t *testing.T) {
	backupName := "ScheduleSet"

	mockHandler := handler{}
	backup := &v1.Backup{
		Spec: v1.BackupSpec{
			Schedule: "@midnight",
		},
	}
	backup.SetName(backupName)

	mockHandler.setBackupType(backup)

	assert.Equal(t, v1.RecurringBackupType, backup.Status.BackupType, "Expected backupType 'Recurring' for backup %s", backup.Name)
}

func TestSetBackupTypeOneTime(t *testing.T) {
	backupOneName := "ScheduleEmpty"
	backupTwoName := "ScheduleNonExisting"

	mockHandler := handler{}
	backupOne := &v1.Backup{
		Spec: v1.BackupSpec{
			Schedule: "",
		},
	}
	backupOne.SetName(backupOneName)

	backupTwo := &v1.Backup{
		Spec: v1.BackupSpec{},
	}
	backupTwo.SetName(backupTwoName)

	mockHandler.setBackupType(backupOne)
	mockHandler.setBackupType(backupTwo)

	assert.Equal(t, v1.OneTimeBackupType, backupOne.Status.BackupType, "Expected backupType 'One-time' for backup %s", backupOne.Name)
	assert.Equal(t, v1.OneTimeBackupType, backupTwo.Status.BackupType, "Expected backupType 'One-time' for backup %s", backupOne.Name)
}

func TestSetBackupTypeRecurringToOneTime(t *testing.T) {
	backupName := "FromRecurringToOneTime"

	// A backup with type Recurring but empty schedule means the user removed the schedule to turn it into a One-time backup
	mockHandler := handler{}
	backup := &v1.Backup{
		Spec: v1.BackupSpec{
			Schedule: "",
		},
		Status: v1.BackupStatus{
			BackupType: v1.RecurringBackupType,
		},
	}
	backup.SetName(backupName)

	mockHandler.setBackupType(backup)

	assert.Equal(t, v1.OneTimeBackupType, backup.Status.BackupType, "Expected backupType 'One-time' for backup %s", backup.Name)
}

func TestSetBackupTypeOneTimeToRecurring(t *testing.T) {
	backupName := "FromOneTimeToRecurring"

	// A backup with type One-time but non-empty schedule means the user added the schedule to turn it into a Recurring backup
	mockHandler := handler{}
	backup := &v1.Backup{
		Spec: v1.BackupSpec{
			Schedule: "@midnight",
		},
		Status: v1.BackupStatus{
			BackupType: v1.OneTimeBackupType,
		},
	}
	backup.SetName(backupName)

	mockHandler.setBackupType(backup)

	assert.Equal(t, v1.RecurringBackupType, backup.Status.BackupType, "Expected backupType 'Recurring' for backup %s", backup.Name)
}

func TestValidateBackupSpecFailByInvalidCron(t *testing.T) {
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
	require.Equal(t, int64(DefaultRetentionCountRecurring), backup.Spec.RetentionCount)
}

func TestValidateBackupSpecPassNonDefaultRetention(t *testing.T) {
	// Expected output: TestName-TestNamespace-2023-05-08T13-40-33-04-00
	// The timestamp is based on time.Now() so we don't check for it explicitly
	backupOneName := "NonDefaultRetentionCount"
	backupTwoName := "InvalidRetentionCount"

	var arbitraryRetentionCount int64 = 7
	var invalidRetentionCount int64 = 0

	mockHandler := handler{}

	backupOne := &v1.Backup{
		Status: v1.BackupStatus{
			BackupType: v1.RecurringBackupType,
		},
		Spec: v1.BackupSpec{
			Schedule:       "0 0 * * *",
			RetentionCount: arbitraryRetentionCount,
		},
	}
	backupOne.SetName(backupOneName)

	backupTwo := &v1.Backup{
		Status: v1.BackupStatus{
			BackupType: v1.RecurringBackupType,
		},
		Spec: v1.BackupSpec{
			Schedule:       "0 0 * * *",
			RetentionCount: invalidRetentionCount,
		},
	}
	backupTwo.SetName(backupTwoName)

	errOne := mockHandler.validateBackupSpec(backupOne)
	errTwo := mockHandler.validateBackupSpec(backupTwo)

	require.NoError(t, errOne, "Error when Validating backup spec for backup %s", backupOne.Name)
	require.NoError(t, errTwo, "Error when Validating backup spec for backup %s", backupTwo.Name)
	require.Equal(t, arbitraryRetentionCount, backupOne.Spec.RetentionCount, "Expected backup %s to maintain the arbitrary retentionCount", backupOne.Name)
	require.Equal(t, int64(DefaultRetentionCountRecurring), backupTwo.Spec.RetentionCount, "Expected backup %s to have retentionCount set to default", backupTwo.Name)
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
	require.Equal(t, backup.Spec.RetentionCount, int64(DefaultRetentionCountOneTime))
}

func TestValidateBackupSpecOneTimeNonDefaultRetention(t *testing.T) {
	backupName := "NonDefaultRetentionCount"

	mockHandler := handler{}

	backup := &v1.Backup{
		Status: v1.BackupStatus{
			BackupType: v1.OneTimeBackupType,
		},
		Spec: v1.BackupSpec{
			RetentionCount: 10,
		},
	}
	backup.SetName(backupName)

	err := mockHandler.validateBackupSpec(backup)

	require.NoError(t, err, "Error when Validating backup spec")
	require.Equal(t, backup.Spec.RetentionCount, int64(DefaultRetentionCountOneTime))
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
