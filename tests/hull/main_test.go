package hull

import (
	"testing"

	"github.com/rancher/hull/pkg/test"
)

// var HelmVersion = flag.String("helm_version", "", "Helm Chart Version")

func TestChart(t *testing.T) {
	opts := test.GetRancherOptions()
	suite.Run(t, opts)
}
