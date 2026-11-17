# Markov v0.2.1 stable candidate

Human explicitly approved creating/pushing tag v0.2.1 at reviewed source
fcb6ce06de57025f4a21ac9d10a11814fff78381. Annotated (not real-key-signed) tag
object d25141eb95b144657c4396e307228a0942d5653d was pushed and peeled remotely
to that exact source. Review-documentation branch remains separate.

Stable clean-tag candidate/native Arm run:
https://github.com/vokramd/markov/actions/runs/44109044662

**Stable unsigned candidate prepared and verified.** Candidate and native Arm
jobs passed; all original candidate checksums passed again after local tests.
Debian 13 amd64, Fedora 45 x86_64 and Arch rolling x86_64 each passed clean
install, v0.2.0 → v0.2.1 upgrade, version, quickstart and removal with preserved
user data. These local copies used disposable fixture signatures only.
Both final OCI architectures passed exact version/binary hash/revision labels
and unprivileged quickstart with persisted host-user workspaces.

## Exact stable inputs

- Version: 0.2.1; ref: refs/tags/v0.2.1; source fcb6ce0.
- Artifact ID: 14340017702; 29 inventory files accounted for.
- Download ZIP SHA256:
  `5245a4ca1691e5503fa8bc58cf3f4857268e62c0d32c28dff8876947992709e2`.
- Candidate manifest SHA256:
  `05c8c2522f2c240cd76a0c4e1a7ad3eee97b0cd9e772c3550659e2fed2dfdfc4`.
- Stable OCI index:
  `sha256:76ab27b0a8b0e820b5925065267e419cc886f1a63115935d7fad7bec02515610`.
- Six SPDX SBOMs checked; 86 dependency license records, no unresolved sources.
- Full source, checksum, platform and native-client evidence:
  [structured receipt](release-v0.2.1-stable.json).

## Unsigned signing preparation

`~/.cache/cc71/v0.2.1/stable/unsigned-signing-work/state.json` is stage prepared,
fixture=false, with pristine native packages and captured RPM header requests.
No GPG signing was performed by this preparation. All original CI bytes are
retained in a private sibling ci-candidate-vault outside formatter mounts.
Capture-only metadata containers have no keyring or SSH-agent mounts and no
network. Final RPM signatures will change RPM bytes; final signed checksums
must be generated after signing, while retaining this unsigned inventory.

## Next approval

Request **local real-key signing only** of these exact v0.2.1 inputs using the
previously selected p15 archive signing subkey
B0BEFAA736416BC0118E5EC33CF4648EB2925C50, under primary
F50B9360C2C9C876476017A33B8A1C64696AECEF. Revalidate current independent backup
custody before key use, then use the approved host-only signing runbook. Do not
record human signing approval or use a private key before that approval arrives.
After signing, verify exact final signatures/native clients and present the
signed bundle/promotion plan for separate public publication approval.

No fresh Omarchy VM or native macOS execution is claimed by this stable check.
Earlier 11-row snapshot and Omarchy evidence remain distinct from these stable
artifact checks.
No real-key use, package publication, Release, GHCR or website changes authorized.
Earlier 11-row snapshot/negatives/runtime evidence remains
[separate](release-v0.2.1-review.md). Stable artifacts will never be substituted
with relabeled snapshot bytes.
