# Prepare Markov v0.2.1 candidates

Human authorized candidate preparation using GitHub Actions, not more markovd
orchestration. Source fcb6ce06de57025f4a21ac9d10a11814fff78381 includes the merged
SQLite concurrency fix. Isolated branch release/v0.2.1-preparation preserves
unrelated original work and examples/quickstart-report.md.

Unsigned workflow_dispatch run:
https://github.com/vokramd/markov/actions/runs/44015543510

Without a tag, the existing workflow builds 0.2.1-dev snapshots. Review these
before requesting permission for v0.2.1 tag/clean-tag CI; production signing
requires that separate clean-tag artifact. Do not relabel snapshot bytes as
final release artifacts. Real-key signing and all public publication remain
gated. Only disposable fixture keys are permitted for local native tests.

## Completed verification

Candidate and native Arm CI passed. Safe artifact download/source verification,
all checksums (including a final recheck), all six SPDX SBOMs/86 source licenses,
OCI platform/digest checks, all 11 serial install/upgrade/removal rows, three
native signature/hash-negative families and both OCI runtime platforms passed.
Exact candidate binary passed eight independent processes against a startup
SQLite write lock; all checkpoints completed. Runtime image hashes/labels match.
66 repository-tool tests and 5 OCI digest tests passed. No host packages installed,
real keys touched, tags created, publication performed or markovd changes made.

[Review packet](../../notes/release-v0.2.1-review.md) and
[structured receipt](../../notes/release-v0.2.1-review.json).
No fresh Omarchy VM run or macOS execution claimed. Review-only docs branch is
separate from source fcb6ce0. Next gate: human approval for v0.2.1 tag and its
clean-tag CI; signing/publication remain separate later approvals.
