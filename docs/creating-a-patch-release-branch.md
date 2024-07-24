## Creating a patch release branch

This doc will cover the how's and when's of creating a minor release branch for BRO. 
Ideally for this repo we will not create these before we need them to reduce branch noise.
So in this repo "patch release branch" should only be created JIT, not preemptively.

### Why would I create a patch release branch?
Technically repos like ours do not have to withhold progress due to the main rancher/rancher code-freeze.
So in theory we can continue merging to our release branch as we please during a release freeze.

Unfortunately, if PRs are merged and need to create a last minute patch release the HEAD branch is no longer valid for this.
As cutting a release from that would include more changes than the small fix a last minute patch would require.

In that event, you would need to create a `release/vX.Y.(Z+1)` branch where all values are defined based on the version being patched.
This new patch release branch would be created based on the original tag being patched.
Finally, you will backport the fix from the HEAD release branch to this patch release branch.

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
