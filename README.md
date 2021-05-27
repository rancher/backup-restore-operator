# Backup and Restore Operator

### Description

* This operator provides ability to backup and restore Kubernetes applications (metadata) running on any cluster. It accepts a list of resources that need to be backed up for a particular application. It then gathers these resources by querying the Kubernetes API server, packages all the resources to create a tarball file and pushes it to the configured backup storage location. Since it gathers resources by quering the API server, it can back up applications from any type of Kubernetes cluster.
* The operator preserves the ownerReferences on all resources, hence maintaining dependencies between objects.
* It also provides encryption support, to encrypt user specified resources before saving them in the backup file. It uses the same encryption configuration that is used to enable [Kubernetes Encryption at Rest](https://kubernetes.io/docs/tasks/administer-cluster/encrypt-data/). Follow the steps in [this section](https://rancher.com/docs/rancher/v2.5/en/backups/configuration/backup-config/#encryption) to configure this.

----

### Quickstart

If Rancher v2.5+ is installed, you can install the `backup-restore-operator`, from the [Cluster Explorer UI](https://rancher.com/docs/rancher/v2.x/en/backups/v2.5/).
Otherwise, you can install the charts via `helm repo` by executing the commands below.

First, add our charts repository.

```bash
helm repo add rancher-charts https://raw.githubusercontent.com/rancher/charts/release-v2.5/
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

### CRDs

It installs the following cluster-scoped CRDs:
#### Backup
  A backup can be performed by creating an instance of the Backup CRD. It can be configured to perform a one-time backup, or to schedule recurring backups. For help configuring backups, see [this documentation.](https://rancher.com/docs/rancher/v2.5/en/backups/configuration/backup-config/)
#### Restore
  Creating an instance of the Restore CRD lets you restore from a backup file. For help configuring backups, see [this documentation.](https://rancher.com/docs/rancher/v2.5/en/backups/configuration/restore-config/)
#### ResourceSet
  ResourceSet specifies the Kubernetes core resources and CRDs that need to be backed up. This chart comes with a predetermined ResourceSet to be used for backing up Rancher application

----

### User flow
1. Create a ResourceSet, that targets all the resources you want to backup. The ResourceSet required for backing up Rancher will be provided and installed by the chart. Refer to the default [rancher-resourceset](https://github.com/rancher/backup-restore-operator/blob/master/charts/rancher-backup/templates/rancher-resourceset.yaml) as an example for creating resourceSets
2. Performing a backup: To take a backup, user has to create an instance of the Backup CRD (create a Backup CR). Each Backup CR must reference a ResourceSet. A Backup CR can be used to perform a one-time backup or recurring backups. Refer [examples](https://github.com/rancher/backup-restore-operator/tree/master/examples) folder for sample manifests
3. Restoring from a backup: To restore from a backup, user has to create an instance of the Restore CRD (create a Restore CR). A Restore CR must contain the exact Backup filename.  Refer [examples](https://github.com/rancher/backup-restore-operator/tree/master/examples) folder for sample manifests

---
### Storage Location

For help configuring the storage location, see the [documentation.](https://rancher.com/docs/rancher/v2.5/en/backups/configuration/storage-config/)

---

### S3 Credentials

If you are using S3 to store your backups, the Backup custom resource can reference an S3 credential secret in any namespace. The `credentialSecretNamespace` directive tells the backup application where to look for the secret:

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
