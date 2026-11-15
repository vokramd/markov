# CC-71 production readiness

Status: current. Human requested continuation after local pilot/Omarchy review.

Prepare reviewable production transfer tooling and operator runbook locally. Verify signed input, retained history, immutable conflicts, package-first/indexes-last ordering, pinned SSH trust, HTTP verification, interruption/retry and publication locking. Tests use disposable keys and mocked transport; no external writes.

Next credential gate: encrypted independent-secret backup and restore on m720q `/mnt/seagate_8tb`, then dedicated deploy-key provisioning and DreamHost staging confinement. Prior explicit ask-first gates remain until the specific actions are approved. No real-key export/signing, m720q access, DreamHost writes, push or publication in this readiness step.

Phase 2 expansion follows acceptance of phase 1; official-distro admission research follows working first-party distribution. Do not treat local snapshots as clean-tag production releases.

## Checkpoint

Prepared `markovd.com/repo-tools/publish.py`, production release runbook and 14 new publisher tests (26 promotion/publication tests total passed). Default review mode makes no network calls; a full-size throwaway fixture generated 108 history, 62 payload, 41 index and 3 APT commit entries under `.cc71-vms/publication-review-1/`. Full snapshot/manifest independently reverified. Explicit execution is still untested against DreamHost and refuses fixtures, missing clean-tag/CI identity or unsafe identity/host pins. It needs staging and publication approval; it does not sign or generate keys.

An initial full-size plan exceeded `/tmp` quota; only that incomplete test directory was removed, then workspace storage succeeded. Added free-space preflight and retained the quota-check requirement (filesystem free space alone cannot establish a user quota). No user cache/artifacts removed. Tests/logs: `/tmp/cc71-publication-tests.log`; no heavy matrix retest needed for these isolated tools.

Next required human input: independent backup secret method (interactive new passphrase on p15 vs existing recovery public key). Asked asynchronously; no secret should be sent in chat. Backup, restored private nonce test signature, deploy-key provisioning and remote staging remain unperformed. Real-key signing/generation integration cannot be treated as verified production tooling yet. No m720q/DreamHost contact in this continuation, and no push/publication.
