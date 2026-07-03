# E2E Test Architecture

This directory contains end-to-end tests with different isolation strategies based on test requirements.

## Directory Structure

```
e2e/
├── backup/               # Regular e2e tests (shared cluster)
│   ├── backup_test.go
│   ├── restore_test.go
│   └── setup_test.go
│
├── fixtures/            # Shared test data across all tests
│   ├── test.go         # Helper to load fixtures
│   └── testdata/       # Go convention: test data files
│       ├── rancher-resource-set-basic.yaml
│       └── restore/
│           └── preserve-unknown-fields.tar.gz
│
├── upgrade-metrics/     # Metrics upgrade tests (own cluster)
│   ├── suite_test.go   # Creates/destroys k3d cluster
│   ├── helpers_test.go
│   ├── metrics_test.go
│   ├── testdata/       # Scenario-specific test data
│   │   └── metrics/
│   │       └── restore-nil-prune.yaml
│   └── README.md
│
├── upgrade-backup/      # Future: backup upgrade tests (own cluster)
└── upgrade-restore/     # Future: restore upgrade tests (own cluster)
```

## Test Isolation Strategies

### Shared Cluster Tests (`e2e/backup/`)

**Pattern**: Multiple tests share a single k3d cluster created by `scripts/testenv`

**Configuration**: Uses environment variables
```go
type TestSpec struct {
    Kubeconfig     string `env:"KUBECONFIG,required"`
    ChartNamespace string `env:"CHART_NAMESPACE,required"`
}
```

**When to use**:
- Tests use the same code version
- Linear test flow (deploy → test → done)
- Predictable state
- Tests don't swap CRDs or Helm versions

**Benefits**:
- ✅ Faster (no cluster creation overhead per test)
- ✅ Simpler (less infrastructure management)

**Example**: `e2e/backup/` - all tests use current code version

### Isolated Cluster Tests (`e2e/upgrade-*/`)

**Pattern**: Each test package creates and destroys its own k3d cluster

**Configuration**: Self-contained, ignores environment variables
```go
var _ = BeforeSuite(func() {
    // Create own k3d cluster
    exec.Command("k3d", "cluster", "create", clusterName, ...)
    
    // Generate own kubeconfig
    kubeconfigPath := fmt.Sprintf("/tmp/%s-kubeconfig.yaml", clusterName)
    ...
})
```

**When to use**:
- Tests install/uninstall Helm charts
- Tests swap CRD versions
- Tests compare old vs new code versions
- Destructive operations that can't be cleaned up
- Different tests need different versions

**Benefits**:
- ✅ Complete isolation - tests can't interfere
- ✅ No cleanup complexity - just destroy the cluster
- ✅ **Can run in parallel with other test packages** (different clusters!)
- ✅ Reliable - guaranteed clean state

**Example**: `e2e/upgrade-metrics/` - swaps CRDs, installs v10.0.5, then tests current code

### Running Both Strategies Together

Because isolated tests **don't read environment variables**, we can run all test packages with a single command:

```bash
# Sets KUBECONFIG and CHART_NAMESPACE for backup tests
# Upgrade tests ignore these and create their own clusters
go test -p 2 ./e2e/...
```

The `-p 2` flag runs packages in parallel since they use different clusters!

## Running Tests

### Run All Tests
```bash
./scripts/e2e
```

This:
1. Sets up shared cluster (for backup tests)
2. Exports `KUBECONFIG` and `CHART_NAMESPACE` environment variables
3. Runs `go test ./e2e/...` which discovers all test packages
4. **Runs tests in parallel** (`-p 2`):
   - Backup tests use the shared cluster (via env vars)
   - Upgrade-metrics tests create their own cluster (ignore env vars)

### Run Specific Test Suite
```bash
# Just backup tests (uses shared cluster)
cd e2e
export KUBECONFIG=../kubeconfig.yaml
export CHART_NAMESPACE=cattle-resources-system
go test -v ./backup/

# Just upgrade-metrics tests (creates own cluster, no env vars needed)
cd e2e
go test -v ./upgrade-metrics/
```

### Run Both in Parallel Manually
```bash
# Setup shared cluster first
./scripts/testenv
k3d kubeconfig get backup-restore > kubeconfig.yaml

# Run all tests with parallelism
cd e2e
export KUBECONFIG=../kubeconfig.yaml
export CHART_NAMESPACE=cattle-resources-system
go test -v -timeout 20m -p 2 ./...
```

### Run By Label
```bash
cd e2e/upgrade-metrics
go test -v -ginkgo.label-filter="SURE-11795"  # Specific JIRA
go test -v -ginkgo.label-filter="metrics"     # All metrics tests
```

## Adding New Upgrade Tests

### Step 1: Create New Package

```bash
mkdir e2e/upgrade-<feature>
```

### Step 2: Copy Template Files

Use `e2e/upgrade-metrics/` as a template:

- `suite_test.go` - Cluster lifecycle and setup
- `helpers_test.go` - Helm install/uninstall helpers
- `<feature>_test.go` - Actual tests
- `testdata/<feature>/` - Test fixtures

### Step 3: Update `suite_test.go`

Change the cluster name to be unique:

```go
var (
    clusterName = "bro-upgrade-<feature>"  // Make unique!
    k3sVersion  = "v1.36.1-k3s1"
)

func Test<Feature>(t *testing.T) {
    RunSpecs(t, "Upgrade <Feature> Suite")
}
```

### Step 4: Update `scripts/e2e`

Add your new test package:

```bash
echo "==> Running upgrade-<feature> e2e tests (isolated cluster)"
cd ../upgrade-<feature>
go test -v -timeout 20m -count=1 ./...
```

### Step 5: Document in Package README

Create `e2e/upgrade-<feature>/README.md` documenting:
- Which JIRA bugs it covers
- Which versions have the bugs
- What the bugs are
- How to run just these tests

## Best Practices

### Shared Fixtures

Put reusable test data in `e2e/fixtures/testdata/`:

```go
import "github.com/rancher/backup-restore-operator/e2e/fixtures"

data := fixtures.Data("rancher-resource-set-basic.yaml")
```

### Scenario-Specific Fixtures

Put scenario-specific data in the test package's `testdata/`:

```go
//go:embed testdata
var testDataFS embed.FS

func loadTestData(feature, filename string) []byte {
    data, _ := fs.ReadFile(testDataFS, path.Join("testdata", feature, filename))
    return data
}
```

### Test Organization

- **Group by feature**, not by JIRA ID
- **Track all JIRAs** in test comments if a bug regresses
- **Use labels** for filtering by JIRA or feature
- **Document clearly** why the test exists

### Cluster Naming

Each isolated test package MUST use a unique cluster name:

```go
clusterName = "bro-upgrade-metrics"   // ✅ Unique
clusterName = "bro-upgrade-backup"    // ✅ Unique  
clusterName = "test-cluster"          // ❌ Conflicts!
```

## Troubleshooting

**k3d cluster already exists**:
```bash
k3d cluster delete bro-upgrade-metrics
```

**Tests interfering with each other**:
- Check if multiple upgrade packages use the same cluster name
- Verify tests are in separate packages (not sharing global state)

**Cluster not cleaning up**:
- Check AfterSuite is running
- Manually delete: `k3d cluster delete <cluster-name>`

**Can't find CRD chart**:
- Run `./scripts/package-helm` first
- Check `build/artifacts/rancher-backup-crd-*.tgz` exists
