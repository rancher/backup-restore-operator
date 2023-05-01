package hull

import "os"

func GetChartVersionFromEnv() string {
	if os.Getenv("GIT_TAG") != "" {
		return "rancher-backup-" + os.Getenv("HELM_VERSION") + ".tgz"
	} else {
		return "rancher-backup-" + os.Getenv("HELM_VERSION_DEV") + ".tgz"
	}
}
