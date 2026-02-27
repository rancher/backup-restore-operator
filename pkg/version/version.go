package version

import "fmt"

var (
	Version   = "v0.0.0-dev"
	GitCommit = "HEAD"
	Date      = "unknown"
)

// FmtVersionInfo returns a formatted version string for the given binary name.
func FmtVersionInfo(binaryName string) string {
	return fmt.Sprintf("%s %s (commit: %s, built: %s)", binaryName, Version, GitCommit, Date)
}
