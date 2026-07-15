# Upgrade & Regression Tests

This directory contains end-to-end tests for upgrade scenarios and regression testing, organized by feature/component area.

## Overview

These tests use a **hybrid approach**:
1. Deploy an old/buggy version of the operator via Helm
2. Create conditions that trigger the bug
3. Verify the bug exists in the old version
4. Stop the Helm deployment
5. Run the current codebase in-process
6. Verify the bug is fixed

## Why This Approach?

- **Realistic**: Uses real released versions to create authentic broken states
- **Fast iteration**: Current code runs in-process for easy debugging
- **No image building**: Tests use published images for old versions
- **Minimal overhead**: Reuses existing e2e test infrastructure

## Organization

Tests are organized by **feature/component area**, not by bug ID. This prevents duplication when the same bug regresses multiple times.

```
upgrade/
├── README.md                # This file
├── suite_test.go            # Shared test suite setup
├── helpers_test.go          # Shared helpers
├── data/                    # Test fixtures by feature area
│   ├── metrics/
│   │   └── restore-nil-prune.yaml
│   ├── backup/
│   └── restore/
├── metrics_test.go          # Metrics-related upgrade scenarios
├── restore_test.go          # Restore-related upgrade scenarios
└── backup_test.go           # Backup-related upgrade scenarios
```

### Tracking JIRA IDs

Use **comments at the test level** to track which JIRA/bug IDs are covered:

```go
// Test: Restore metrics with nil Prune field
// Covers: TEAM-12435, TEAM-98765  // Add new IDs if bug regresses
// Bug: Nil pointer crash in metrics collection
// Location: pkg/monitoring/metrics.go:154
Describe("Restore metrics with nil pointer fields", func() {
    // ... test implementation
})
```

**Benefits**:
- Same test can track multiple bug IDs if it regresses
- Tests grouped by logical similarity
- Can still filter by JIRA ID using labels
- Prevents duplicate tests for the same underlying issue

### Naming Convention

- **Test files**: `{feature}_test.go` (e.g., `metrics_test.go`, `restore_test.go`)
- **Data directories**: `data/{feature}/` (e.g., `data/metrics/`, `data/backup/`)
- **Ginkgo labels**: Feature area + JIRA IDs: `Label("upgrade", "metrics", "TEAM-12435")`
- **Describe blocks**: Feature-focused: `Describe("Metrics upgrade scenarios")`

## Running Tests

```bash
# Set up environment
export KUBECONFIG=~/.kube/config
export CHART_NAMESPACE=cattle-resources-system

# Run all upgrade tests
cd e2e/upgrade
go test -v -timeout 30m

# Run tests for a specific JIRA ID
ginkgo -v --label-filter="TEAM-12435" .

# Run all metrics-related tests
ginkgo -v --label-filter="metrics" .

# Run all upgrade/regression tests
ginkgo -v --label-filter="upgrade" .
```

## Test Cases

### Metrics Tests (`metrics_test.go`)

#### Restore metrics with nil Prune field
- **Covers**: TEAM-12435
- **Bug**: Nil pointer crash when Restore.Spec.Prune is nil during metrics collection
- **Occurs**: Upgrading from v9 to v10 with completed in-place restores
- **Location**: `pkg/monitoring/metrics.go:154`
- **Run**: `ginkgo -v --label-filter="TEAM-12435" .`

## Adding New Regression Tests

### Step 1: Determine Feature Area

Choose the appropriate feature file or create a new one:
- Metrics issues → `metrics_test.go`
- Backup issues → `backup_test.go`
- Restore issues → `restore_test.go`
- New area → `{feature}_test.go`

### Step 2: Add Test Data (if needed)

```bash
# Create feature directory if it doesn't exist
mkdir -p data/{feature}

# Add test fixtures
cat > data/{feature}/your-fixture.yaml <<EOF
apiVersion: resources.cattle.io/v1
kind: SomeResource
# ...
EOF
```

### Step 3: Add Test to Feature File

```go
// Test: Brief description of what's being tested
// Covers: TEAM-12435  // Add your JIRA ID(s) here
// Bug: Detailed description of the bug
// Occurs: When does this happen (upgrade path, conditions, etc.)
// Location: path/to/buggy/code.go:123
Describe("Specific scenario description", Ordered, 
    Label("upgrade", "regression", "{feature}", "TEAM-12435"), func() {
    
    var o *ObjectTracker
    
    const (
        buggyVersion   = "vX.Y.Z"  // Version that has the bug
        deploymentName = "rancher-backup"
    )
    
    BeforeAll(func() {
        o = &ObjectTracker{arr: []client.Object{}, mu: sync.Mutex{}}
        DeferCleanup(func() { o.DeleteAll() })
        
        deployOperatorViaHelm(buggyVersion)
        waitForDeploymentReady(deploymentName)
    })
    
    AfterAll(func() {
        uninstallOperator()
    })
    
    Context("when bug conditions exist", func() {
        It("should exhibit bug in old version", func() {
            // Create conditions, verify bug exists
        })
        
        It("should be fixed in current version", func() {
            scaleDeploymentToZero(deploymentName)
            errC, cancel := SetupOperator(testCtx, restCfg, ...)
            DeferCleanup(func() { cancel() })
            
            // Verify bug is fixed
        })
    })
})
```

### Step 4: Document in README

Add a section under the appropriate feature area describing:
- JIRA ID(s) covered
- Bug description
- When it occurs
- Code location
- How to run just this test

### Step 5: Handling Regressions

If the same bug happens again with a new JIRA ID:

```go
// Test: Restore metrics with nil Prune field
// Covers: TEAM-12435, TEAM-99999  // <-- Just add the new ID
// Bug: Nil pointer crash in metrics collection
// Note: Regressed in v11.2.0, fixed again in v11.2.1
Describe("Restore metrics with nil pointer fields", func() {
    // ... same test, don't duplicate
})
```

## Prerequisites

- Running Kubernetes cluster (kind, k3d, minikube, etc.)
- Helm installed
- CRDs installed in the cluster
- Environment variables set:
  ```bash
  export KUBECONFIG=/path/to/kubeconfig
  export CHART_NAMESPACE=cattle-resources-system
  ```

## Tips

- **Group by feature**: Keep related tests together in the same file
- **Track all IDs**: Add all JIRA IDs that a test covers to the "Covers:" comment
- **Use labels**: Add feature and JIRA ID labels for easy filtering
- **Document clearly**: Future you (or teammates) should understand why the test exists
- **Avoid duplication**: If a bug regresses, update the existing test, don't create a new one

## Troubleshooting

**Helm install fails**:
- Verify the version exists: `helm search repo rancher/backup-restore-operator --versions`
- Check chart path is correct: `../../charts/rancher-backup`

**Pod doesn't crash as expected**:
- Verify the test data correctly replicates the bug condition
- Check pod logs: `kubectl logs -n cattle-resources-system <pod-name>`

**In-process operator conflicts**:
- Ensure Helm deployment is scaled to zero before starting in-process
- Check no other operator instances are running
