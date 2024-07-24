## Creating a patch release branch

This doc will cover the how's and when's of creating a minor release branch for BRO. 
Ideally for this repo we will not create these before we need them to reduce branch noise.
So in this repo "patch release branch" should only be created JIT, not preemptively.

### What is a patch release branch?
A patch release branch is simply a branch targeting a specific patch version.

An example of this would be the [`release/v2.8.5`](https://github.com/rancher/rancher/tree/release/v2.8.5) branch on the `rancher/rancher` repo.
As this branch exists so that the team could produce a `v2.8.5` that only fixes upon `v2.8.4`.
Rather than include all the changes included in `release/v2.8` at the time which may target a later release.

### Why would I create a patch release branch?
During the release process of `rancher/rancher` there are phases when we cannot merge PRs to that repo.
And often other repositories honor that freeze as well to simplify things if a patch were needed.

Yet, observing that code freeze can reduce forward progress for repos like this one.
The primary benefit to observing the freeze is it avoids needing and using a process like this.
However, if a process is established then a squad/repo should be able to continue forward progress.

### When would I create a patch release branch?

Essentially, if PRs are merged during a time when `r/r` is frozen and we need to create a last minute patch release.
In this situation, the HEAD branch is no longer valid for this as cutting a release would include more changes.
And we should only ship a small fix in this patch release required to fix the issue.

In that event, you would create a `release/vX.Y.(Z+1)` branch based on the version needing a patch.
First you would implement the patch on the `release/vX.Y` branch and verify it fixes the issue.
Finally, you will backport that fix from the HEAD release branch to this new patch release branch.

### How do I create a patch release branch?

Best demonstrated by example, lets assume we need to patch BRO `v5.0.0` and don't want any new changes in `release/v5.0`.
How do we make those moves?

1. Start by checking out the tag being patched: ```git checkout tags/v5.0.0```
2. Now create a new patch branch from this spot: ```git checkout -b release/v5.0.1```
3. Push the new patch branch to the BRO repo.

Once the new branch is pushed to Rancher remote repo you should PR a backport to it.

In general treat this the same way as a normal release branch for older release versions.
Meaning we should only backport to these branches and never forward-port from them to others.

### How do I release from a patch release branch?

Follow the normal process however, the only version that we will ever release from the branch is the one it mathches.
In the example above, we should only produce `v5.0.1` based releases (alphas, betas, RCs, and stable).
The patch release branch should not be used to produce any other release versions.
