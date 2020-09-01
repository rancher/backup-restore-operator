package crds

import (
	"encoding/json"
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
			return c.
				WithColumn("Storage-Location", ".status.storageLocation").
				WithColumn("Backup-Type", ".status.backupType").
				WithColumn("Backups-Saved", ".status.numSnapshots").
				WithColumn("Backupfile-Prefix", ".status.prefix").
				WithColumn("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&resources.Restore{}, func(c crd.CRD) crd.CRD {
			return c.
				WithColumn("Retries", ".status.numRetries").
				WithColumn("Backup-Source", ".status.backupSource").
				WithColumn("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&resources.ResourceSet{}, func(c crd.CRD) crd.CRD {
			return c
		}),
	}
}

func customizeBackup(backup *apiext.CustomResourceDefinition) {
	properties := backup.Spec.Validation.OpenAPIV3Schema.Properties
	spec := properties["spec"]
	spec.Required = []string{"resourceSetName"}
	resourceSetName := spec.Properties["resourceSetName"]
	resourceSetName.Description = "Name of the ResourceSet CR to use for backup, must be in the same namespace as the operator"
	spec.Properties["resourceSetName"] = resourceSetName
	encryptionConfig := spec.Properties["encryptionConfigName"]
	encryptionConfig.Description = "Name of the Secret containing the encryption config, must be in the same namespace as the operator"
	spec.Properties["encryptionConfigName"] = encryptionConfig
	schedule := spec.Properties["schedule"]
	schedule.Description = "Cron schedule for recurring backups"
	examples := make(map[string]interface{})
	examples["Standard crontab specs"] = "* * * * ?"
	examples["Descriptors"] = "@midnight"
	byteArr, err := json.Marshal(examples)
	if err == nil {
		schedule.Example = &apiext.JSON{Raw: byteArr}
	}
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
	maxDeleteTimeout := float64(10)
	properties := restore.Spec.Validation.OpenAPIV3Schema.Properties
	spec := properties["spec"]
	spec.Required = []string{"backupFilename"}
	deleteTimeout := spec.Properties["deleteTimeout"]
	deleteTimeout.Maximum = &maxDeleteTimeout
	spec.Properties["deleteTimeout"] = deleteTimeout
	properties["spec"] = spec
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
