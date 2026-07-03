package version

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_FmtVersionInfo(t *testing.T) {
	versionInfo := FmtVersionInfo("bro")
	assert.Equal(t, "bro v0.0.0-dev (commit: HEAD, built: unknown)", versionInfo)
}
