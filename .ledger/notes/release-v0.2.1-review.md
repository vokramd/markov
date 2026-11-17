# Markov v0.2.1 preparation review

Status: **unsigned pre-tag snapshot review complete; ready for human review**. No production signing or
public publication. Source `fcb6ce06de57025f4a21ac9d10a11814fff78381`.

## Intended release notes

This patch improves SQLite checkpoint reliability when multiple Markov CLI
processes share a state file. Each connection now waits up to five seconds for
competing writes (explicit URI timeout settings are honored), and startup
schema inspection/migrations are serialized in an immediate transaction.
Workflow effects are not retried, and journal mode remains unchanged. A lock
held past the timeout still fails. PostgreSQL remains appropriate for sustained
concurrent daemon workloads.

Engine only; no markovd, harness or UI packages. Package formats, dependencies,
repository URL contract, first-party status and phase-1 matrix are unchanged.

## Review inputs and gates

- Unsigned Actions run: https://github.com/vokramd/markov/actions/runs/44015543510
- Workflow dispatch produces `0.2.1-dev`, not final stable v0.2.1.
- Real-key signing requires separate approval and artifacts from successful
  clean-tag/native Arm CI after an approved v0.2.1 tag. Do not relabel snapshots.
- Public native repository promotion, GitHub Release, GHCR and site changes
  require separate authorization; no such actions occur in this preparation.
- Full engine tests/race state checks passed during source-fix review. GitHub
  PR checks, including native Arm, passed before merge. This run independently
  checks the merged source candidate.
- Existing repository-tool suite: 66 tests passed during preparation.
- OCI index/digest helper suite: all 5 tests passed.
- No dependency, packaging definition, workflow or container baseline changes
  since v0.2.0; the executable delta is the two SQLite source/test files.
- All old unsigned v0.2.0 candidate checksums verified before generating an
  isolated upgrade fixture signed by a newly generated disposable test key.

## Candidate evidence

- Actions candidate and native-arm64 jobs both passed for merged source fcb6ce0.
- Artifact 14299211967 downloaded with redirect credentials stripped; bounded
  ZIP extraction checked link/special/traversal/duplicate/unlisted entries;
  no unsafe entries were found.
- ZIP SHA256: `c1b7a643e14d418f0b9c7276ea60134ff0d783c902d2a3e4b0475b2ba5a3ee64`.
- Candidate manifest SHA256:
  `e8eb247a104c192671801a245d895c32c13563ed0385198e5fe9ea15a29d41de`.
- The 28-file inventory is fully accounted for; all listed checksums passed; clean snapshot identity matches the
  GitHub API run/head. This is not a signed attestation or reproducibility claim.
- Five native packages have expected architecture/dependencies. deb/RPM snapshot
  version is 0.2.1~dev; Arch recipe version is 0.2.1dev. CLI is 0.2.1-dev.
- Six SPDX SBOMs parse with package inventories; 86 dependency license records,
  no unresolved license sources. Both OCI platform manifests/hash references and
  index sidecar validate. Snapshot index digest:
  `sha256:4f78840356f73c95f173b98a5dff6d49254b1466e2ebbdc92027bdcbdad079f2`.
- Exact downloaded amd64 binary completed eight concurrent independent CLI
  invocations sharing a SQLite file held under a 250 ms startup write lock:
  eight completed runs/eight completed steps, no lost checkpoints.
- **All 11 native container rows passed** install, upgrade from 0.2.0,
  version, quickstart and removal while preserving user files. Arm package
  rows ran under existing QEMU emulation; native Arm CLI CI passed separately.
- **APT, RPM and pacman** each rejected corrupted metadata signatures and
  corrupted package bytes in isolated negative views.
- **OCI amd64 and arm64** each passed exact version, validation and quickstart
  as the host user with a persisted workspace. Image binary hashes match the
  exact candidate binaries; revision/version labels match the source/snapshot.
- All original candidate checksums passed again after tests. Local loopback
  origin and test containers stopped. Persistent data is under home cache;
  shared /tmp is 3% used, with no candidate/fixture outputs placed there.
- Metadata image ID:
  `sha256:ccba524bc3a9322c582e5c465d74870e075c958615ff1ff8ba5f472037a70476`.
- [Structured review receipt](release-v0.2.1-review.json) records complete rows,
  digests, source/CI identity, binary contention and package metadata.

| Target | Architectures | Snapshot upgrade/install/remove result |
| --- | --- | --- |
| Debian 13 | amd64, arm64 | Passed |
| Ubuntu 24.04 | amd64, arm64 | Passed |
| Ubuntu 26.04 | amd64, arm64 | Passed |
| Fedora 44 | x86_64, aarch64 | Passed |
| Fedora 45 | x86_64, aarch64 | Passed |
| Arch rolling | x86_64 | Passed |
| Omarchy rolling | x86_64 | Earlier VM evidence; no fresh snapshot VM run |

No fresh Omarchy VM validation is claimed by container matrix results; required
Arch container validation and earlier Omarchy acceptance are distinct evidence.

## Next approved-source release steps

1. Human approval to create/push **v0.2.1** at exactly
   `fcb6ce06de57025f4a21ac9d10a11814fff78381`. The review-only ledger branch is
   separate; its documentation commit is not substituted for the tested source.
2. Let tag-triggered Actions build stable unsigned packages/OCI and complete
   native Arm validation. Download to a separate retained tag-candidate vault,
   verify source/ref/run, checksums, SBOMs/licenses and native stable clients.
3. Prepare the stable bundle with existing release-bundle.py; review artifacts,
   verify current independent key-backup custody, then obtain separate real-key
   signing approval. Sign on p15; never pass keys to CI or metadata containers.
4. Validate exact signed Debian/RPM/pacman artifacts and repository metadata;
   build a local promotion plan retaining all historical package versions.
5. Review final stable image digest/install-page changes and obtain public
   publication approval. Promote packages/history, indexes, InRelease last;
   publish Release/GHCR separately and verify anonymous public clients.

The snapshot OCI digest above belongs to 0.2.1-dev and must not be advertised
as the final stable image digest. No fresh Omarchy VM or native macOS execution
is claimed for this snapshot; earlier Omarchy acceptance remains historical.

## Subsequent approved tag

Human approved v0.2.1 tag creation; clean-tag CI and stable unsigned verification
completed separately. Use [stable candidate review](release-v0.2.1-stable.md)
for signing inputs; this snapshot document and its OCI digest remain historical.
