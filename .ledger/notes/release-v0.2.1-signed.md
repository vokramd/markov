# Markov v0.2.1 signed local review

Human authorized archive-subkey signing. **GUI passphrase entry succeeded.**
Signing used the existing dedicated p15 keyring and explicitly selected subkey;
the adjacent stored passphrase was not read. Dedicated Wayland Qt pinentry was
configured; no key identity/password changed or key material copied.

Before key use, independent tag/CI/native Arm and artifact/license checks were
repeated. Read-only m720q verification confirmed the encrypted backup remains
on mounted /dev/sda1 at /mnt/seagate_8tb; its hash matched the previously
restored-and-private-sign-tested receipt. Preserve it during CC-48 migration.

## Verified outputs

- Source fcb6ce0, tag v0.2.1, clean-tag CI 44109044662.
- signed-and-verified, fixture=false; five shared native package artifacts.
- Fresh public-only GPG keyring verified checksums and release manifest with
  subkey B0BEFAA736416BC0118E5EC33CF4648EB2925C50 under primary
  F50B9360C2C9C876476017A33B8A1C64696AECEF. No secret key in verifier.
- All 23 final checksummed payloads passed; rpmkeys verified both RPM
  architectures. Exact signed Debian/Fedora/Arch install/upgrade/version/
  quickstart/removal passed serially, preserving user files.
- OCI index sha256:76ab27b0a8b0e820b5925065267e419cc886f1a63115935d7fad7bec02515610
  is covered by signed checksums. Signing left the tested OCI bytes intact.
- Local package plan: 108 history, 62 payload, 41 index and 3 InRelease paths.
  Keep all old versions; payload/history first, indexes next, InRelease last.
- Site local commit 982caac updates install-page version/links/digest and
  maintainer notes; the site plan uploads only install/index.html.
  Site manifest SHA256:
  35eafe9d42b6090a858e9b195f9fd30d976095535ce7f9e76bcf74cadc2b6dee.
  Code-block Bash syntax and release/digest checks passed.
- Read-only pinned DreamHost check confirmed the intended repos directory and
  filesystem capacity (567 GiB free). No hosting writes/config changes.

[Structured signed receipt](release-v0.2.1-signed.json) records final native
hashes, signed manifest and client receipts. The original unsigned vault is
retained. Signed outputs are under
~/.cache/cc71/v0.2.1/stable/unsigned-signing-work/{bundle,final-artifacts};
the historical directory name does not change its signed-and-verified state.

## Publication gate

No package, Release, GHCR or site publication performed or authorized. Review
repo-tools/V0.2.1-PUBLICATION-REVIEW.md and RELEASE-NOTES-V0.2.1.md in
/home/jtanner/workspace/github/vokramd/markovd.com-v0.2.1-review . Proposed
publication covers native repos, GitHub Release assets/notes, exact GHCR
:0.2.1 plus :latest, and one install-page file. No DNS, server configuration,
private-key transfer, deploy-key or production-daemon changes are included.
Recheck immutable conflicts/current origin state immediately before execution.
