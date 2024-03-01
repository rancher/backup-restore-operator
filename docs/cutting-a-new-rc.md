## Cutting a new RC

To generate the assets we go through git. Eli: do this on the rancher repo of the chart
Eric: I do this in my fork and submit a PR to the rancher repo

### 2.6.

```
cd backup-restore-operator
git checkout release/v2.0
git remote add release git@github.com:rancher/backup-restore-operator.git
git pull release release/v2.0
# check if commits are up2date
git log -
git tag v2.1.5
git push release v2.1.5
# watch build on https://drone-publish.rancher.io/rancher/backup-restore-operator

```
### 2.7.

```
cd backup-restore-operator
git checkout release/v3.0
git remote add release git@github.com:rancher/backup-restore-operator.git
git pull release release/v3.0
# check if commits are up2date
git log -
git tag v3.1.0-rc2
git push release v3.1.0-rc2
# watch build on https://drone-publish.rancher.io/rancher/backup-restore-operator
```

```
### 2.8.

```
cd backup-restore-operator # (rancher repo)
git checkout release/v4.0
git pull release/v4.0
# check if commits are up2date
git log -
git tag v4.0.1-rc4
git push v4.0.1-rc4
# watch build on https://drone-publish.rancher.io/rancher/backup-restore-operator
```

Then edit the release on github to add the changelog.

## Updating rancher/charts

First remove the old rc and commit. 

### Please note!!!!! 

Don’t remove an old version if that version is an
official release i.e. only delete old rc versions

```
make remove CHART=rancher-backup VERSION=103.0.1+up4.0.1-rc3
make remove CHART=rancher-backup-crd VERSION=103.0.1+up4.0.1-rc3
```

### Make sure you don’t forget to do this for both the main chart and the `crd`

Then update `packages/rancher-backup/rancher-backup*/package.yaml` (there may be more than one) and `release.yaml` to point to the new rc.

For reference see this commit:
https://github.com/rancher/charts/pull/1878/commits/8e41f8dc1f15033178abda9774181afa813d

```
rancher-backup:
- 103.0.0+up4.0.1-rc4

rancher-backup-crd:
- 103.0.0+up4.0.1-rc4

rancher-backup:
- v4.0.1-rc4

rancher-backup-crd:
- v4.0.1-rc4
```

After this, commit again

And finally:

```
make charts PACKAGE=rancher-backup
```

and commit again.


# Don't know why, but we need to bring in the main release branch or validate will fail to pull it

```
git fetch upstream
git co release-v2.8
```

### double check everything with `make validate`


