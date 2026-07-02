package main

import (
	"os"

	controllergen "github.com/rancher/wrangler/v3/pkg/controller-gen"
	"github.com/rancher/wrangler/v3/pkg/controller-gen/args"
)

func main() {
	_ = os.Unsetenv("GOPATH")

	controllergen.Run(args.Options{
		OutputPackage: "github.com/rancher/backup-restore-operator/pkg/generated",
		Boilerplate:   "scripts/boilerplate.go.txt",
		Groups: map[string]args.Group{
			"resources.cattle.io": {
				Types: []interface{}{
					// All structs with an embedded ObjectMeta field will be picked up
					"./pkg/apis/resources.cattle.io/v1",
				},
				GenerateTypes:   true,
				GenerateOpenAPI: true,
				OpenAPIDependencies: []string{
					"k8s.io/apimachinery/pkg/apis/meta/v1",
				},
			},
		},
	})
}
