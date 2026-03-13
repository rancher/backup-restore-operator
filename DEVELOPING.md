# Developing

Developer tips, tricks, and workflows for the [rancher/backup-restore-operator](https://github.com/rancher/backup-restore-operator).

### Go Workspace (go.work)

This repository uses a [Go workspace](https://go.dev/ref/mod#workspaces) (`go.work`) to manage three independent Go modules together:

| Module | Path |
|---|---|
| `github.com/rancher/backup-restore-operator` | `.` (root) |
| `github.com/rancher/backup-restore-operator/cmd/tool` | `./cmd/tool` |
| `github.com/rancher/backup-restore-operator/tests` | `./tests` |

The workspace means you can run `go` commands from the repository root and have them resolve cross-module dependencies without needing to publish anything.

#### Day-to-day commands

```bash
# Build everything in the workspace (operator + bro-tool)
make build

# Build just bro-tool (from repo root via workspace)
go build -C cmd/tool -o bin/bro-tool .

# Test everything in the workspace
go test ./...

# Tidy all modules and sync go.work.sum in one step
make tidy

# Tidy a single module manually (run from that module's directory)
cd cmd/tool && go mod tidy

# Update go.work.sum after any go.mod change
go work sync
```

#### Adding dependencies

Always add dependencies to the **correct module's** `go.mod`, not the root. The rule of thumb:

- Dependency only used by `bro-tool` → `cd cmd/tool && go get <pkg>`
- Dependency only used by integration tests → `cd tests && go get <pkg>`
- Dependency used by the operator itself → `go get <pkg>` from the root

Never add `bro-tool`- or test-only dependencies to the root module. The root module is the BRO operator binary; bloating it affects the production image.

#### Workspace vs. standalone

The `go.work` file is checked in and should always be present for local development. Each sub-module also carries a `replace` directive in its own `go.mod` (e.g. `cmd/tool/go.mod` replaces the root module with `../../`) so the modules still resolve correctly in contexts where `go.work` is not active — such as individual module CI jobs or `go get` from external consumers.

If you ever need to test a module in true standalone mode (without the workspace), use `GOWORK=off`:

```bash
GOWORK=off go build ./...
```

#### IDE / gopls

Most editors using `gopls` pick up `go.work` automatically and provide full cross-module navigation and type checking. If your editor shows unresolved imports across modules, check that gopls is running from the repository root (where `go.work` lives), not from inside a sub-module directory.

---

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
