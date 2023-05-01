package hull

import (
	"flag"
	"testing"

	"github.com/rancher/hull/pkg/test"
)

// var HelmVersion = flag.String("helm_version", "", "Helm Chart Version")

func TestChart(t *testing.T) {
	flag.Parse()
	opts := test.GetRancherOptions()
	suite.Run(t, opts)
}
