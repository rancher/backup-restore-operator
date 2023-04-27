package hull

import (
	"flag"
	"testing"

	"github.com/rancher/hull/pkg/test"
)

func TestChart(t *testing.T) {
	flag.Parse()
	opts := test.GetRancherOptions()
	suite.Run(t, opts)
}
