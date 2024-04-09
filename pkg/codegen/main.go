package main

import (
	"os"

	v1 "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/backup-restore-operator/pkg/crds"
	controllergen "github.com/rancher/wrangler/v2/pkg/controller-gen"
	"github.com/rancher/wrangler/v2/pkg/controller-gen/args"
)

func main() {
	os.Unsetenv("GOPATH")
	controllergen.Run(args.Options{
		OutputPackage: "github.com/rancher/backup-restore-operator/pkg/generated",
		Boilerplate:   "scripts/boilerplate.go.txt",
		Groups: map[string]args.Group{
			"resources.cattle.io": {
				Types: []interface{}{
					v1.Backup{},
					v1.ResourceSet{},
					v1.Restore{},
				},
				GenerateTypes: true,
			},
		},
	})
	err := crds.WriteCRD()
	if err != nil {
		panic(err)
	}
}
