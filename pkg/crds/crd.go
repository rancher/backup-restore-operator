package crds

import (
	"fmt"
	"io/ioutil"
	"strings"

	resources "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	_ "github.com/rancher/wrangler-api/pkg/generated/controllers/apiextensions.k8s.io/v1beta1" // Imported to use init function
	"github.com/rancher/wrangler/pkg/crd"
	"github.com/rancher/wrangler/pkg/yaml"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func WriteCRD() error {
	for _, crdDef := range List() {
		bCrd, err := crdDef.ToCustomResourceDefinition()
		if err != nil {
			return err
		}
		switch bCrd.Name {
		case "backups.resources.cattle.io":
			customizeBackup(&bCrd)
		case "resourcesets.resources.cattle.io":
			customizeResourceSet(&bCrd)
		case "restores.resources.cattle.io":
			customizeRestore(&bCrd)
		}

		yamlBytes, err := yaml.Export(&bCrd)
		if err != nil {
			return err
		}

		filename := fmt.Sprintf("./crds/%s.yaml", strings.ToLower(bCrd.Spec.Names.Kind))
		err = ioutil.WriteFile(filename, yamlBytes, 0644)
		if err != nil {
			return err
		}
	}
	return nil
}

func List() []crd.CRD {
	return []crd.CRD{
		newCRD(&resources.Backup{}, func(c crd.CRD) crd.CRD {
			return c
		}),
		newCRD(&resources.ResourceSet{}, func(c crd.CRD) crd.CRD {
			return c
		}),
		newCRD(&resources.Restore{}, func(c crd.CRD) crd.CRD {
			return c
		}),
	}
}

func customizeBackup(backup *apiext.CustomResourceDefinition) {
	properties := backup.Spec.Validation.OpenAPIV3Schema.Properties
	spec := properties["spec"]
	spec.Required = []string{"resourceSetName"}
	//retentionCount := spec.Properties["retentionCount"]
	//defaultRetention, _ := json.Marshal(10)
	//retentionCount.Default = &apiext.JSON{Raw: defaultRetention}
	//spec.Properties["retentionCount"] = retentionCount
	resourceSetName := spec.Properties["resourceSetName"]
	resourceSetName.Description = "Name of resourceSet CR to use for backup, must be in the same namespace"
	spec.Properties["resourceSetName"] = resourceSetName
	encryptionConfig := spec.Properties["encryptionConfigName"]
	encryptionConfig.Description = "Name of secret containing the encryption config, must be in the namespace of the chart: cattle-resources-system"
	spec.Properties["encryptionConfigName"] = encryptionConfig
	schedule := spec.Properties["schedule"]
	schedule.Description = "Cron schedule for recurring backups"
	spec.Properties["schedule"] = schedule
	properties["spec"] = spec
}

func customizeResourceSet(resourceSetCRD *apiext.CustomResourceDefinition) {
	resourceSet := resourceSetCRD.Spec.Validation.OpenAPIV3Schema
	resourceSet.Required = []string{"resourceSelectors"}
	resourceSelector := resourceSet.Properties["resourceSelectors"]
	resourceSelector.Required = []string{"apiVersion"}
	resourceSet.Properties["resourceSelectors"] = resourceSelector
}

func customizeRestore(restore *apiext.CustomResourceDefinition) {
	maxDeleteTimeout := float64(5)
	properties := restore.Spec.Validation.OpenAPIV3Schema.Properties
	spec := properties["spec"]
	deleteTimeout := spec.Properties["deleteTimeout"]
	deleteTimeout.Maximum = &maxDeleteTimeout
	spec.Properties["deleteTimeout"] = deleteTimeout
}

func newCRD(obj interface{}, customize func(crd.CRD) crd.CRD) crd.CRD {
	crd := crd.CRD{
		GVK: schema.GroupVersionKind{
			Group:   "resources.cattle.io",
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
