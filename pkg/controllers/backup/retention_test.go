package backup

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_backupFileRegexp(t *testing.T) {
	const ns = "24e1b8ce-1f00-4bbe-94bb-248ad7606dc8"
	const stamp = "2026-10-26T10-00-00Z"

	tests := []struct {
		name      string
		backup    string
		encrypted bool
		key       string
		match     bool
	}{
		{name: "own file", backup: "daily", key: "daily-" + ns + "-" + stamp + ".tar.gz", match: true},
		{name: "own encrypted file", backup: "daily", encrypted: true, key: "daily-" + ns + "-" + stamp + ".tar.gz.enc", match: true},
		{name: "encrypted file of a plain backup", backup: "daily", key: "daily-" + ns + "-" + stamp + ".tar.gz.enc", match: false},
		{name: "plain file of an encrypted backup", backup: "daily", encrypted: true, key: "daily-" + ns + "-" + stamp + ".tar.gz", match: false},
		{name: "another backup whose name ends with this one", backup: "daily", key: "rancher-daily-" + ns + "-" + stamp + ".tar.gz", match: false},
		{name: "another backup whose name starts with this one", backup: "daily", key: "daily-full-" + ns + "-" + stamp + ".tar.gz", match: false},
		{name: "name with a regular expression character matches itself", backup: "backup.v2", key: "backup.v2-" + ns + "-" + stamp + ".tar.gz", match: true},
		{name: "name with a regular expression character does not match others", backup: "backup.v2", key: "backupxv2-" + ns + "-" + stamp + ".tar.gz", match: false},
		{name: "same name in another management cluster", backup: "daily", key: "daily-ffffffff-1f00-4bbe-94bb-248ad7606dc8-" + stamp + ".tar.gz", match: false},
		{name: "file in a nested folder", backup: "daily", key: "nested/daily-" + ns + "-" + stamp + ".tar.gz", match: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re := backupFileRegexp(tt.backup, ns, tt.encrypted)
			assert.Equal(t, tt.match, re.MatchString(tt.key), "pattern %s against %s", re, tt.key)
		})
	}
}

func Test_backupFileName(t *testing.T) {
	assert.Equal(t, "daily-x.tar.gz", backupFileName("daily-x.tar.gz", ""))
	assert.Equal(t, "daily-x.tar.gz", backupFileName("backups/daily-x.tar.gz", "backups"))
	assert.Equal(t, "nested/daily-x.tar.gz", backupFileName("backups/nested/daily-x.tar.gz", "backups"))
	assert.Equal(t, "backups-old/daily-x.tar.gz", backupFileName("backups-old/daily-x.tar.gz", "backups"))
}
