# Backup and Restore Operator

## Latest release

[![Latest](https://img.shields.io/badge/dynamic/yaml?label=backup-restore-operator&query=%24.entries%5B%27rancher-backup%27%5D%5B0%5D.appVersion&url=https%3A%2F%2Fcharts.rancher.io%2Findex.yaml)](https://github.com/rancher/backup-restore-operator/releases/latest)

## Description

The Backup and Restore Operator provides the ability to back up and restore the Rancher application running on any Kubernetes cluster.

### Use Cases
- Performing a backup before upgrading Rancher and restoring after a failed upgrade.
- Restoring your *Rancher application* to a new cluster in a disaster recovery scenario.
- Migrating your *Rancher application* between Kubernetes distributions of the same version.
- (Optional) Storing and restoring backups using [Kubernetes Encryption at Rest](https://kubernetes.io/docs/tasks/administer-cluster/encrypt-data/).

What the Backup Restore Operator is not:
- A downstream cluster snapshot tool,
- A replacement for Etcd cluster backups,
- Configured to back up user-created resources on the Rancher cluster.

### Branches and Releases
This is the current branch strategy for `rancher/backup-restore-operator`, it may change in the future.

| Branch         | Tag      | Rancher                |
|----------------|----------|------------------------|
| `main`         | `head`   | `main` branch (`head`) |
| `release/v6.0` | `v6.x.x` | `v2.10.x`              |
| `release/v5.0` | `v5.x.x` | `v2.9.x`               |
| `release/v4.0` | `v4.x.x` | `v2.8.x`               |

----

## Quickstart

You will need to install the `backup-restore-operator`, from the [Cluster Explorer UI](https://ranchermanager.docs.rancher.com/pages-for-subheaders/backup-restore-and-disaster-recovery).
Within the App catalog look for the `Rancher Backups` application chart.

However, when performing a Rancher migration you will not have the UI installed.  
So, you will need to install the charts via `helm repo` by executing the commands below.

First, add the `rancher-charts` charts repository.

```bash
helm repo add rancher-charts https://charts.rancher.io
helm repo update
```

Then, install both charts.
Ensure that the CRD chart is installed first.

```bash
helm install --wait \
    --create-namespace -n cattle-resources-system \
    rancher-backup-crd rancher-charts/rancher-backup-crd
helm install --wait \
    -n cattle-resources-system \
    rancher-backup rancher-charts/rancher-backup
```

If you are using S3, you can configure the `s3.credentialSecretNamespace` to determine where the Backup and Restore Operator will look for the S3 backup secret. For more information on configuring backups, see the [backup documentation](https://ranchermanager.docs.rancher.com/how-to-guides/new-user-guides/backup-restore-and-disaster-recovery/back-up-rancher#2-perform-a-backup).

----

### Uninstallation

If you are uninstalling and want to keep backup(s), ensure that you have created Backup CR(s) and that your backups are stored in a safe location.
Execute the following commands to uninstall:

```bash
helm uninstall -n cattle-resources-system rancher-backup
helm uninstall -n cattle-resources-system rancher-backup-crd
kubectl delete namespace cattle-resources-system
```

----

## More Info

The default chart is built for the use case of backing up and restoring the Rancher application.
However, under the hood the Backup Restore Operator is a rather flexible extension for backup and restore of Kubernetes resources.

* This operator provides the ability to backup and restore Kubernetes applications (metadata) running on any cluster. It accepts a list of resources that need to be backed up for the application. It then gathers these resources by querying the Kubernetes API server, packages all the resources to create a tarball file, and pushes it to the configured backup storage location. Since it gathers resources by querying the API server, it can back up applications from any type of Kubernetes cluster.
* The operator preserves the `ownerReferences` on all resources, hence maintaining dependencies between objects.
* It also provides encryption support, to encrypt user specified resources before saving them in the backup file. It uses the same encryption configuration that is used to enable [Kubernetes Encryption at Rest](https://kubernetes.io/docs/tasks/administer-cluster/encrypt-data/). Follow the steps in [this section](https://ranchermanager.docs.rancher.com/reference-guides/backup-restore-configuration/backup-configuration#encryption) to configure this.

### CRDs

It installs the following cluster-scoped CRDs:
#### Backup
  A backup can be performed by creating an instance of the Backup CRD. It can be configured to perform a one-time backup, or to schedule recurring backups. For help configuring backups, see [this documentation](https://ranchermanager.docs.rancher.com/reference-guides/backup-restore-configuration/backup-configuration).
#### Restore
  Creating an instance of the Restore CRD lets you restore from a backup file. For help configuring restores, see [this documentation](https://ranchermanager.docs.rancher.com/reference-guides/backup-restore-configuration/restore-configuration).
#### ResourceSet
  ResourceSet specifies the Kubernetes core resources and CRDs that need to be backed up. This chart comes with a predetermined ResourceSet to be used for backing up Rancher application

----

### User flow
1. Create a ResourceSet, that targets all the resources you want to backup. The ResourceSet required for backing up Rancher will be provided and installed by the chart. Refer to the default [rancher-resourceset](https://github.com/rancher/backup-restore-operator/blob/master/charts/rancher-backup/templates/rancher-resourceset.yaml) as an example for creating resourceSets
2. Performing a backup: To take a backup, user has to create an instance of the Backup CRD (create a Backup CR). Each Backup CR must reference a ResourceSet. A Backup CR can be used to perform a one-time backup or recurring backups. Refer [examples](https://github.com/rancher/backup-restore-operator/tree/master/examples) folder for sample manifests
3. Restoring from a backup: To restore from a backup, user has to create an instance of the Restore CRD (create a Restore CR). A Restore CR must contain the exact Backup filename. Refer to the [examples](https://github.com/rancher/backup-restore-operator/tree/master/examples) folder for sample manifests.

---
### Storage Location

For help configuring the storage location, see [this documentation](https://ranchermanager.docs.rancher.com/reference-guides/backup-restore-configuration/storage-configuration).

---

### S3 Credentials

If you are using S3 to store your backups, the `Backup` custom resource can reference an S3 credential secret in any namespace. The `credentialSecretNamespace` directive tells the backup application where to look for the secret:

```
s3:
  bucketName: ''
  credentialSecretName: ''
  credentialSecretNamespace: ''
  enabled: false
  endpoint: ''
  endpointCA: ''
  folder: ''
  insecureTLSSkipVerify: false
  region: ''
```

---

### Developer Documentation

Refer to [DEVELOPING.md](./DEVELOPING.md) for developer tips, tricks, and workflows when working with the `backup-restore-operator`.

### Troubleshooting

Refer to [troubleshooting.md](./docs/troubleshooting.md) for troubleshooting commands.
