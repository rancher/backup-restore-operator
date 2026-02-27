# bro-tool

`bro-tool` is a CLI helper for the [backup-restore-operator (BRO)](../../README.md). It provides one-off operations for managing backup and restore resources outside of the operator's control loop.

## Versioning and compatibility

`bro-tool` is built and versioned alongside BRO. Each release of `bro-tool` is fully compatible with the BRO version it was built for.

**Newer tool, older BRO:** A newer `bro-tool` can generally interact with older BRO installations and data. Logical compatibility is maintained across versions where possible.

**Older tool, newer BRO:** An older `bro-tool` is only expected to work with newer BRO versions within the same major version. Cross-major-version use is unsupported.

In short: prefer keeping `bro-tool` at the same version as your BRO installation, and upgrade the tool before downgrading it.

## Usage

```
bro-tool [flags] <command> [command flags]
```

Run `bro-tool --help` to list available commands and flags.

### Subcommands

1. `bro-tool resource-set:view` - View the resources managed by a specific BRO version.
   - `--version` - The BRO version to view - will pull from GitHub or Charts repo.
   - `--path` - The path to the rancher backup chart to use.
   - `--output` - Output format: yaml, json, or table
2. `bro-tool resource-set:check` - Validate that a specific k8s resource is covered by a ResourceSet rule
   - `--version` - The BRO version to check against - will pull from GitHub or Charts repo.
   - `--path` - The path to the rancher backup chart to use. 
   - `--resource` - The k8s resource to check - should be in the format "kind/name"
   - `--resource-path` - The path to the k8s resource to check - should be in the format `path/to/resource.yaml
3. `bro-tool resource-set:diff` - Compare two ResourceSets and identify differences in managed resources
   - Takes two positional arguments: Either a version or path
     - First argument: The base to compare against
     - Second argument: The target to compare against

### How

Each command should follow a similar pattern:
- Parse inputs and validate,
- If needed fetch remote resources (use a bro-tool cache dir when possible),
- Render the helm template and extract the ResourceSet resources,
- Extract the actual Rules from the ResourceSet resources,
- Do sub-command specific work.

So that should help define what core/foundation code this new subpackage needs.
As that should be easy to share between all the sub-commands we have in mind so far.

And obviously, the new dependency of helm and such can apply only to the tool due to our multi-module setup.

- The `resource-set:view` subcommand is basically just the core workflow + pretty print options.
- The `resource-set:check` subcommand is the core workflow + taking user input to do a matcher validation.
- The `resource-set:diff` subcommand is the core workflow + do it again with another version + diffing both.

## Dev Rules

Anything for the new bro tool alone should be added to `cmd/tool/go.mod` only and not other go modules.
None of the bro-tool code is considered part of the BRO project; rather, bro-tool is a consumer of BRO built along side it.
The bro-tool code is not considered a public API for reuse outside bro-tool.
The bro-tool is provided as-is and is not intended for production use – it is a tool for development, testing and debugging.