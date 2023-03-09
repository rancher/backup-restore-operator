package hull

import (
	"testing"

	"github.com/rancher/hull/pkg/test"
)

func TestChart(t *testing.T) {
	opts := test.GetRancherOptions()
	// opts.Coverage.IncludeSubcharts = true
	// opts.Coverage.Disabled = true
	suite.Run(t, opts)
}
