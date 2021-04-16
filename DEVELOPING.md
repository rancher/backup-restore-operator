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

#### Optional: Use a Personal Repository for Testing

You can also build and push the operator to a personal repository for use with the chart.

```bash
NAME=<org>/<name>
TAG=<version>
IMAGE=$NAME:$TAG

make build
docker build -t $IMAGE .
docker push $IMAGE
```

Now, you can use that image for the chart during installation.

```bash
--set image.repository=$NAME --set image.tag=$TAG
```

### Developer Uninstallation

Follow the uninstallation instructions in [README.md](./README.md).
