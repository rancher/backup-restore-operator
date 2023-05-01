package hull

import (
	"os"
)

func GetChartVersionFromEnv() string {
	return "rancher-backup-" + os.Getenv("CHART_VERSION") + ".tgz"
}
