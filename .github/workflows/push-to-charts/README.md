# push-to-charts

Automates opening PRs against [rancher/charts](https://github.com/rancher/charts) when a new BRO release is published.

## Trigger

The workflow fires on `release: published` (after goreleaser uploads chart artifacts) and can also be dispatched manually with an explicit tag.

Because `release: published` always runs from the default branch, **no backporting is needed** — a single copy of these scripts handles all version lines via `CHARTS_BRANCH_MAP` in `common.sh`.

## Adding a new BRO/Rancher version pair

Edit `common.sh` and add an entry to `CHARTS_BRANCH_MAP`:

```bash
["10"]="dev-v2.14"
```

## Local usage

```bash
./.github/workflows/push-to-charts/run-local.sh \
  --charts-dir /path/to/rancher/charts \
  --tag v9.0.2-rc.5 \
  [--dry-run] \
  [--remote upstream]
```

`--dry-run` runs all local git work (commits to your charts clone) but skips push and PR creation.

## Step sequence

| Script | What it does |
|---|---|
| `remove-previous-rc.sh` | `make remove` for the previous RC charts version. No-op if base BRO version changed or old version was not a prerelease. |
| `update-package-yaml.sh` | Updates `url` and `additionalCharts[0].upstreamOptions.url` to the new release artifacts. Bumps charts version (minor or patch) unless the base BRO version is unchanged (RC→RC or RC→stable). |
| `update-patches.sh` | Updates `appVersion` in `Chart.yaml.patch`, then runs `make prepare`, `make patch`, `make clean`. |
| `generate-assets.sh` | Runs `make charts USE_CACHE=true`. |
| `update-release-yaml.sh` | Prepends the new combined version (`{charts_version}+up{bro_version}`) to `rancher-backup` and `rancher-backup-crd` entries in `release.yaml`. |
| `create-pr.sh` | Pushes the branch and opens a PR against the target branch in rancher/charts. |

## Version bump rules

| Transition | Charts version |
|---|---|
| `9.0.1` → `9.0.2-rc.1` | patch bump: `108.0.1` → `108.0.2` |
| `9.0.2-rc.1` → `9.0.2-rc.5` | no bump: stays `108.0.2` |
| `9.0.2-rc.5` → `9.0.2` | no bump: stays `108.0.2` |
| `9.0.2` → `9.1.0-rc.1` | minor bump: `108.0.2` → `108.1.0` |

## Key env vars

| Var | Description |
|---|---|
| `TAG` | BRO release tag (e.g. `v9.0.2-rc.5`) |
| `CHARTS_DIR` | Path to local rancher/charts clone |
| `CHARTS_REMOTE` | Remote name in `CHARTS_DIR` (default: `origin`) |
| `DRY_RUN` | Set to `true` to skip push and PR creation |
| `SOURCE_REPO` | Source repo for PR body (default: `rancher/backup-restore-operator`) |

## GHA prerequisites

The workflow reads a GitHub App credential from Vault at:

```
secret/data/github/repo/rancher/backup-restore-operator/github/app-credentials
```

The app must have write access to `rancher/charts` to push branches and open PRs.
