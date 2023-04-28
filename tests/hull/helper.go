package hull

import (
	"flag"
)

var helm_version string

func GetChartFileNameWithVersion() string {
	flag.StringVar(&helm_version, "helm_version", "", "Helm Chart Version")
	if helm_version == "" {
		helm_version = "0.0.0-dev"
	}
	return "rancher-backup-" + helm_version + ".tgz"
}
