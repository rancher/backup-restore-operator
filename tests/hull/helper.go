package hull

import (
	"os"
)

func GetChartVersionFromEnv() string {
	switch droneBuildEvent := os.Getenv("DRONE_BUILD_EVENT"); droneBuildEvent {
	case "push":
		return "rancher-backup-" + os.Getenv("HELM_VERSION") + ".tgz"
	case "pull_request":
		return "rancher-backup-" + os.Getenv("HELM_VERSION_DEV") + ".tgz"
	case "tag":
		return "rancher-backup-" + os.Getenv("HELM_VERSION") + ".tgz"
	default:
		return "rancher-backup-" + os.Getenv("HELM_VERSION") + ".tgz"
	}
}
