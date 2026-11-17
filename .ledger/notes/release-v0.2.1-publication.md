# v0.2.1 approved publication

Human explicitly approved native repos, GitHub Release, GHCR :0.2.1/:latest
and reviewed install-page publication after signed-artifact review.

Historical in-progress checkpoint (superseded by live closeout):
- GHCR :0.2.1 and :latest public, both anonymously resolve to signed index
  sha256:76ab27b0a8b0e820b5925065267e419cc886f1a63115935d7fad7bec02515610.
- Native promotion completed: all ordered passes and final public hashes
  verified. Retained v0.2.0 signed history is being used for real public upgrades.
- GitHub Release 448891576 remains draft with 26 curated assets; all server
  digests/sizes match the approved local artifacts.
- Install page remains at the captured reviewed v0.2.0 baseline until all
  advertised public services pass acceptance. Unrelated site/config/key hashes
  captured; no hosting config, authorized_keys, DNS or service changes.
- Serial 11-row public upgrades/removal and fresh anonymous OCI/runtime
  acceptance are in progress. Full receipts in
  ~/.cache/cc71/v0.2.1/stable/publication .

The local registry helper was adapted to the designated token path's symlink:
only that path is opened and the descriptor must be regular, owner-only 600.
No alternate credentials searched, token logged or credential persisted.
HTTP acceptance uses the previously approved all-revalidation policy; payload
Range response is 206. No hosting configuration changed to alter caching.

If interrupted, inspect current Release/registry/origin state before any retry;
do not overwrite versioned artifacts or blindly repeat writes.

## Completed

All approved targets are published and verified. See
[live closeout](release-v0.2.1-live.md). No publication work remains for v0.2.1.
