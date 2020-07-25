package controllers

import (
	"fmt"
	v1 "github.com/mrajashree/backup/pkg/apis/backupper.cattle.io/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sconfig "k8s.io/apiserver/pkg/apis/config"
	"k8s.io/apiserver/pkg/server/options/encryptionconfig"
	"k8s.io/apiserver/pkg/storage/value"
)

const (
	OldUIDReferenceLabel = "backupper.cattle.io/old-uid"
)

func GetEncryptionTransformers(config *v1.BackupEncryptionConfig) (map[schema.GroupResource]value.Transformer, error) {
	resourceToPrefixTransformer := map[schema.GroupResource][]value.PrefixTransformer{}

	// For each entry in the configuration
	for _, resourceConfig := range config.Resources {
		k8sresourceConfig := k8sconfig.ResourceConfiguration{Resources: resourceConfig.Resources}
		k8sresourceConfig.Providers = make([]k8sconfig.ProviderConfiguration, len(resourceConfig.Providers))
		for ind, providers := range resourceConfig.Providers {
			if providers.AESCBC != nil {
				k8sresourceConfig.Providers[ind].AESCBC = &k8sconfig.AESConfiguration{Keys: getK8sKeys(providers.AESCBC.Keys)}
			}
			if providers.AESGCM != nil {
				k8sresourceConfig.Providers[ind].AESGCM = &k8sconfig.AESConfiguration{Keys: getK8sKeys(providers.AESGCM.Keys)}
			}
			if providers.Secretbox != nil {
				k8sresourceConfig.Providers[ind].Secretbox = &k8sconfig.SecretboxConfiguration{Keys: getK8sKeys(providers.Secretbox.Keys)}
			}
			if providers.KMS != nil {
				kms := providers.KMS
				k8sresourceConfig.Providers[ind].KMS = &k8sconfig.KMSConfiguration{
					Name:      kms.Name,
					CacheSize: kms.CacheSize,
					Endpoint:  kms.Endpoint,
					Timeout:   kms.Timeout,
				}
			}
		}
		transformers, err := encryptionconfig.GetPrefixTransformers(&k8sresourceConfig)
		if err != nil {
			return map[schema.GroupResource]value.Transformer{}, err
		}

		// For each resource, create a list of providers to use
		for _, resource := range resourceConfig.Resources {
			gr := schema.ParseGroupResource(resource)
			resourceToPrefixTransformer[gr] = append(
				resourceToPrefixTransformer[gr], transformers...)
		}
	}

	result := map[schema.GroupResource]value.Transformer{}
	for gr, transList := range resourceToPrefixTransformer {
		result[gr] = value.NewMutableTransformer(value.NewPrefixTransformers(fmt.Errorf("no matching prefix found"), transList...))
	}
	return result, nil
}

func getK8sKeys(rancherKeys []v1.Key) []k8sconfig.Key {
	var k8sKeys []k8sconfig.Key
	for _, rancherkey := range rancherKeys {
		name := rancherkey.Name
		secret := rancherkey.Secret
		k8sKeys = append(k8sKeys, k8sconfig.Key{Name: name, Secret: secret})
	}
	return k8sKeys
}
