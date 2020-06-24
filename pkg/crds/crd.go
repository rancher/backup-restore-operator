package crds

import (
	"fmt"
	//"fmt"
	backupper "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	_ "github.com/rancher/wrangler-api/pkg/generated/controllers/apiextensions.k8s.io"
	"github.com/rancher/wrangler/pkg/crd"
	"github.com/rancher/wrangler/pkg/yaml"
	"io/ioutil"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func WriteCRD() error {
	for _, crdDef := range List() {
		bCrd, err := crdDef.ToCustomResourceDefinition()
		if err != nil {
			fmt.Printf("\nerr converting to CRD: %v\n", bCrd)
			return err
		}
		yamlBytes, err := yaml.Export(&bCrd)
		if err != nil {
			fmt.Printf("\nerr from yaml export: %v\n", err)
			return err
			//panic(err)
		}

		err = ioutil.WriteFile("./crds/crd.yaml", yamlBytes, 0644)
		if err != nil {
			return err
		}
	}
	return nil
}

func List() []crd.CRD {
	return []crd.CRD{
		newCRD(&backupper.Backup{}, func(c crd.CRD) crd.CRD {
			return c
		}),
	}
}

func newCRD(obj interface{}, customize func(crd.CRD) crd.CRD) crd.CRD {
	crd := crd.CRD{
		GVK: schema.GroupVersionKind{
			Group:   "backupper.cattle.io",
			Version: "v1",
		},
		Status:       true,
		SchemaObject: obj,
	}
	if customize != nil {
		crd = customize(crd)
	}
	return crd
}
