# Developing

Developer tips, tricks, and workflows for the [rancher/backup-restore-operator](https://github.com/rancher/backup-restore-operator).

### Developer Installation

To test your local changes, `cd` to the root of the cloned repository, and execute the following commands:

```bash
helm install --wait \
    --create-namespace -n cattle-resources-system \
    rancher-backup-crd ./charts/rancher-backup-crd
helm install --wait \
    -n cattle-resources-system \
    rancher-backup ./charts/rancher-backup
```

### Developer Uninstallation

Follow the uninstallation instructions in [README.md](./README.md).
