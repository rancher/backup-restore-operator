## Integration testing

### Requirements

- [mc](https://min.io/docs/minio/linux/reference/minio-mc.html), a command line client for minio
- [k3d](https://k3d.io/v5.6.3/), a command line tool for managing k3s clusters in docker

See CI install scripts in `./.github/workflows/scripts/`

### Running

Set up a test cluster:

```bash
CLUSTER_NAME="test-cluster" ./.github/workflows/scripts/setup-cluster.sh
```

Run:

```bash
./scripts/integration
```
