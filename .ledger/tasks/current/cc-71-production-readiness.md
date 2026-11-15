# CC-71 production readiness

Status: current. Human requested continuation after local pilot/Omarchy review.

Prepare reviewable production transfer tooling and operator runbook locally. Verify signed input, retained history, immutable conflicts, package-first/indexes-last ordering, pinned SSH trust, HTTP verification, interruption/retry and publication locking. Tests use disposable keys and mocked transport; no external writes.

Next credential gate: encrypted independent-secret backup and restore on m720q `/mnt/seagate_8tb`, then dedicated deploy-key provisioning and DreamHost staging confinement. Prior explicit ask-first gates remain until the specific actions are approved. No real-key export/signing, m720q access, DreamHost writes, push or publication in this readiness step.

Phase 2 expansion follows acceptance of phase 1; official-distro admission research follows working first-party distribution. Do not treat local snapshots as clean-tag production releases.

## Checkpoint

Prepared `markovd.com/repo-tools/publish.py`, production release runbook and 14 new publisher tests (26 promotion/publication tests total passed). Default review mode makes no network calls; a full-size throwaway fixture generated 108 history, 62 payload, 41 index and 3 APT commit entries under `.cc71-vms/publication-review-1/`. Full snapshot/manifest independently reverified. Explicit execution is still untested against DreamHost and refuses fixtures, missing clean-tag/CI identity or unsafe identity/host pins. It needs staging and publication approval; it does not sign or generate keys.

An initial full-size plan exceeded `/tmp` quota; only that incomplete test directory was removed, then workspace storage succeeded. Added free-space preflight and retained the quota-check requirement (filesystem free space alone cannot establish a user quota). No user cache/artifacts removed. Tests/logs: `/tmp/cc71-publication-tests.log`; no heavy matrix retest needed for these isolated tools.

Next required human input: independent backup secret method (interactive new passphrase on p15 vs existing recovery public key). Asked asynchronously; no secret should be sent in chat. Backup, restored private nonce test signature, deploy-key provisioning and remote staging remain unperformed. Real-key signing/generation integration cannot be treated as verified production tooling yet. No m720q/DreamHost contact in this continuation, and no push/publication.

## Backup execution authorization and preflight

Human replied “proceed” to the concrete backup continuation on 2026-11-14. Chose the recommended interactive new symmetric backup passphrase; never request a secret in chat. Existing p15 known_hosts ED25519 pin for `192.168.1.3`: `SHA256:r9jJKP4dW5Qu1onoMH8AEpg/ZpBWKd5f9kRchmpnWM0`; verified via StrictHostKeyChecking=yes, UpdateHostKeys=no, existing desktop agent. Host confirmed `m720q.lab.net`, user jtanner. `/mnt/seagate_8tb` is an actual ext4 mount from `/dev/sda1`, writable, owned jtanner; ~1.1 TB available, ~198 million free inodes. Existing backup convention is `BACKUPS/`; dedicated private destination proposed for this execution is `BACKUPS/markov-signing/p15-2026-11-15T021449Z/`. No independent project-specific CC-70 convention was found locally; retain explicit CC-70/CC-48 references in receipt/recovery notes.

Private backup helper/session is under `.cc71-vms/`, outside Git. It verified both real signing fingerprints, exported protected primary/subkey material into process memory, and opened fresh `pinentry-qt` on p15 DISPLAY :0 for independent AES256 symmetric encryption. Encrypted backup includes original archive passphrase as an encrypted-only member, with keys/revocation/recovery notes; the independent new backup passphrase is never persisted. Current checkpoint: waiting for first human passphrase entry; no ciphertext transfer or restore has completed. A second fresh prompt follows retrieval for restore verification; private restored-subkey nonce sign/verify must pass before marking backup verified. Pending desktop input is not a failed restore, and no publication gate is lifted.
