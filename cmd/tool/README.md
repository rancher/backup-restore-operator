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