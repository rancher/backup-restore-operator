package crds

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"strings"

	resources "github.com/rancher/backup-restore-operator/pkg/apis/resources.cattle.io/v1"
	"github.com/rancher/wrangler/v2/pkg/crd"
	_ "github.com/rancher/wrangler/v2/pkg/generated/controllers/apiextensions.k8s.io/v1" // Imported to use init function
	"github.com/rancher/wrangler/v2/pkg/yaml"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func WriteCRD() error {
	for _, crdDef := range List() {
		bCrd, err := crdDef.ToCustomResourceDefinition()
		if err != nil {
			return err
		}
		newObj, _ := bCrd.(*unstructured.Unstructured)
		var crd apiext.CustomResourceDefinition
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(newObj.Object, &crd); err != nil {
			return err
		}
		switch crd.Name {
		case "backups.resources.cattle.io":
			customizeBackup(&crd)
		case "resourcesets.resources.cattle.io":
			customizeResourceSet(&crd)
		case "restores.resources.cattle.io":
			customizeRestore(&crd)
		}

		yamlBytes, err := yaml.Export(&crd)
		if err != nil {
			return err
		}

		filename := fmt.Sprintf("./charts/rancher-backup-crd/templates/%s.yaml", strings.ToLower(crd.Spec.Names.Kind))
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
				WithColumn("Location", ".status.storageLocation").
				WithColumn("Type", ".status.backupType").
				WithColumn("Latest-Backup", ".status.filename").
				WithColumn("ResourceSet", ".spec.resourceSetName").
				WithCustomColumn(apiext.CustomResourceColumnDefinition{Name: "Age", Type: "date", JSONPath: ".metadata.creationTimestamp"}).
				WithColumn("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&resources.Restore{}, func(c crd.CRD) crd.CRD {
			return c.
				WithColumn("Backup-Source", ".status.backupSource").
				WithColumn("Backup-File", ".spec.backupFilename").
				WithCustomColumn(apiext.CustomResourceColumnDefinition{Name: "Age", Type: "date", JSONPath: ".metadata.creationTimestamp"}).
				WithColumn("Status", ".status.conditions[?(@.type==\"Ready\")].message")
		}),
		newCRD(&resources.ResourceSet{}, func(c crd.CRD) crd.CRD {
			return c
		}),
	}
}

func customizeBackup(backup *apiext.CustomResourceDefinition) {
	for _, version := range backup.Spec.Versions {
		properties := version.Schema.OpenAPIV3Schema.Properties
		//properties := backup.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties
		spec := properties["spec"]
		spec.Required = []string{"resourceSetName"}
		resourceSetName := spec.Properties["resourceSetName"]
		resourceSetName.Description = "Name of the ResourceSet CR to use for backup"
		spec.Properties["resourceSetName"] = resourceSetName
		encryptionConfig := spec.Properties["encryptionConfigSecretName"]
		encryptionConfig.Description = "Name of the Secret containing the encryption config"
		spec.Properties["encryptionConfigSecretName"] = encryptionConfig
		schedule := spec.Properties["schedule"]
		schedule.Description = "Cron schedule for recurring backups"
		examples := make(map[string]interface{})
		examples["Standard crontab specs"] = "0 0 * * *"
		examples["Descriptors"] = "@midnight"
		byteArr, err := json.Marshal(examples)
		if err == nil {
			schedule.Example = &apiext.JSON{Raw: byteArr}
		}
		spec.Properties["schedule"] = schedule
		minRetentionCount := float64(1)
		retentionCount := spec.Properties["retentionCount"]
		retentionCount.Minimum = &minRetentionCount
		spec.Properties["retentionCount"] = retentionCount
		properties["spec"] = spec
	}
}

func customizeResourceSet(resourceSetCRD *apiext.CustomResourceDefinition) {
	for _, version := range resourceSetCRD.Spec.Versions {
		resourceSet := version.Schema.OpenAPIV3Schema
		resourceSet.Required = []string{"resourceSelectors"}
		resourceSelector := resourceSet.Properties["resourceSelectors"]
		resourceSelector.Required = []string{"apiVersion"}
		resourceSet.Properties["resourceSelectors"] = resourceSelector
	}
}

func customizeRestore(restore *apiext.CustomResourceDefinition) {
	for _, version := range restore.Spec.Versions {
		maxDeleteTimeout := float64(10)
		properties := version.Schema.OpenAPIV3Schema.Properties
		spec := properties["spec"]
		spec.Required = []string{"backupFilename"}
		deleteTimeout := spec.Properties["deleteTimeoutSeconds"]
		deleteTimeout.Maximum = &maxDeleteTimeout
		spec.Properties["deleteTimeoutSeconds"] = deleteTimeout
		properties["spec"] = spec
	}
}

func newCRD(obj interface{}, customize func(crd.CRD) crd.CRD) crd.CRD {
	crd := crd.CRD{
		GVK: schema.GroupVersionKind{
			Group:   "resources.cattle.io",
			Version: "v1",
		},
		Status:       true,
		SchemaObject: obj,
		NonNamespace: true,
	}
	if customize != nil {
		crd = customize(crd)
	}
	return crd
}
