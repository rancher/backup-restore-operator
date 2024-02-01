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

### Developer Help Script
For help with setting up the environment, check out `/scripts/deploy`. It has some helpful functions for deploying the various components used in the development environment.

To see all available options, run `./scripts/deploy`.

#### Example Usage

Once your development environment has been set up (i.e. have a cluster up and running), start with exporting the kubeconfig file. From there you can deploy Minio onto the cluster and then backup-restore. Once both charts have been installed you can create a backup. 

```bash
export KUBECONFIG=`pwd`/kubeconfig.yaml

./scripts/deploy minio

./scripts/deploy backup-restore

./scripts/deploy create-backup
```

Some changes done to the chart must be packaged into a new docker image. The difficulty with deploying the updated image is that it must be imported to each node and pulled from the local container registry. A workaround is to publish the image to a public docker repo and point the chart to the new image repo. This is handled in the deploy script and can be done using a simple command flow. To publish through our script you must have your docker hub username exported to be used for the repo.

```bash
export DOCKERHUB_USER=[your username here]

make build

make package

./scripts/deploy publish

./scripts/deploy minio

./scripts/deploy backup-restore

./scripts/deploy create-backup
```

Note: if `DOCKERHUB_USER` is exported then the script will set the image repo to pull from your dockerhub, if not it will use the Public Rancher image instead.

If you want to interact with the Minio deployed in the cluster, you can use the following arguments:

* `list-minio-files`: List all the files currently stored in the bucket `rancherbackups` in Minio
* `retrieve-minio-files`: Copy all the files currently stored in the bucket `rancherbackups` in Minio to the local directory `minio-files-$EPOCH`
* `copy-minio-files`: Copy all the files currently stored in a local directory (passed as first argument) to the bucket `rancherbackups` in Minio
* `reset-minio-bucket`: Delete and create the `rancherbackups` bucket

### Building on Different Architectures

Currently, we only support building this chart fully for `amd64` nodes. The binary and image building scripts will build "arch native" by default.
So when building on `amd64` the resulting binary and image will both be `amd64` - same for `arm64`. This is how the drone builds operate.

We have also included a flag for users running different architectures when trying to package the code into an `amd64` docker image. Building the binary with `CROSS_ARCH=true` set will cause it to build `amd64` and `arm64` binaries, and then setting `USE_DOCKER_BUILDX=1` will force the package script to use docker buildx, setting the container's target platform to build for `amd64`. If you do not have `docker-buildx` installed please reference [this page](https://docs.docker.com/buildx/working-with-buildx/).


### Developer Uninstallation

Follow the uninstallation instructions in [README.md](./README.md).
