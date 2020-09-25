# Backup and Restore Operator


### Description

* This operator provides ability to backup and restore Kubernetes applications (metadata) running on any cluster. It accepts a list of resources that need to be backed up for a particular application. It then gathers these resources by querying the Kubernetes API server, packages all the resources to create a tarball file and pushes it to the configured backup storage location. Since it gathers resources by quering the API server, it can back up applications from any type of Kubernetes cluster.
* The operator preserves the ownerReferences on all resources, hence maintaining dependencies between objects. 
* It also provides encryption support, to encrypt user specified resources before saving them in the backup file. It uses the same encryption configuration that is used to enable [Kubernetes Encryption at Rest](https://kubernetes.io/docs/tasks/administer-cluster/encrypt-data/). Follow the steps in [documentation link TBD] to configure this.

----

### CRDs

It installs the following cluster-scoped CRDs:
#### Backup
  A backup can be performed by creating an instance of the Backup CRD. It can be configured to perform a one-time backup, or to schedule recurring backups.
#### Restore  
  Creating an instance of the Restore CRD lets you restore from a backup file. 
#### ResourceSet  
  ResourceSet specifies the Kubernetes core resources and CRDs that need to be backed up. This chart comes with a predetermined ResourceSet to be used for backing up Rancher application

----

### User flow
1. Create a ResourceSet, that targets all the resources you want to backup. The ResourceSet required for backing up Rancher will be provided and installed by the chart. Refer to the default [rancher-resource-set](https://github.com/rancher/backup-restore-operator/blob/master/crds/rancher-resource-set.yaml) as an example for creating resourceSets
2. Performing a backup: To take a backup, user has to create an instance of the Backup CRD (create a Backup CR). Each Backup CR must reference a ResourceSet. A Backup CR can be used to perform a one-time backup or recurring backups. Refer [examples](https://github.com/rancher/backup-restore-operator/tree/master/examples) folder for sample manifests
3. Restoring from a backup: To restore from a backup, user has to create an instance of the Restore CRD (create a Restore CR). A Restore CR must contain the exact Backup filename.  Refer [examples](https://github.com/rancher/backup-restore-operator/tree/master/examples) folder for sample manifests
