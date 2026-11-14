# Engine release candidates

CC-71 builds the Markov engine only. All publication, deploy-key provisioning, production-key use and VM creation still require separate approval. Existing macOS/Linux tarballs remain supported; the new native channels are a local pilot, not live distro repositories.

## Toolchain and local builds

`go.mod` pins Go 1.25.13; GoReleaser 2.20.2 and Syft 1.57.1 are checksum-pinned in `scripts/release/tools.py`. Bootstrap into a user-local directory, not the system:

```sh
python3 scripts/release/tools.py --directory /tmp/markov-tools
export PATH=/tmp/markov-tools:$PATH
export GOTOOLCHAIN=go1.25.13
export GOCACHE=/tmp/markov-go-cache
export GOPATH=/tmp/markov-go
make lint test
goreleaser check
MARKOV_CANDIDATE_VERSION=0.1.1 goreleaser release --snapshot --clean --skip=publish
python3 scripts/release/arch-package.py dist/markov_0.1.1_linux_amd64.tar.gz --version 0.1.1 --output dist
python3 scripts/release/candidate_manifest.py dist
```

Snapshots with 0.1.0/0.1.1 above are disposable fixture versions, not tags or releases. Do not publish them. GoReleaser builds Linux/darwin amd64/arm64 static binaries with trimpath, archives/source tarball, per-archive SPDX SBOM and SHA-256 checksums; nFPM emits two deb and two rpm artifacts, with Bash/CA dependencies and no Go/libc dependency. Runnable quickstart lives under `/usr/share/markov/examples/`, since slim Ubuntu images deliberately omit `/usr/share/doc`. Arch makepkg wraps the same amd64 archive; it does not rebuild the binary. The helper runs unprivileged without network inside a pinned Arch image, with checksum-pinned source archive and root-owned installed package paths. No AUR submission is performed.

A clean CI checkout is required for a real candidate. The source archive comes from Git, not arbitrary working files; untracked developer state must not enter archives/build contexts. `.gitattributes` excludes the operational ledger/research from Git source archives; local snapshot fixtures are not approved production artifacts. Candidate manifest records commit/ref/run identity and final artifact checksums. Review SBOM/licenses before public distribution. Reproducible tool pins do not by themselves prove bit-for-bit builds; record comparisons before claiming that.

## Workflow trust boundary

`release.yml` produces unsigned Actions artifacts on tags/manual invocation. Token has contents:read only; GoReleaser release publishing is disabled and `--skip=publish` is explicit. No secrets/GHCR login/remote signing or site upload. PR CI is also read-only; action revisions and release tools are pinned. A later publisher must verify approved tag/commit/run/artifact hashes, not consume arbitrary PR artifacts or caches. No tag/push/workflow dispatch is part of this local pilot.

`container-candidate.py` reuses exact Linux package binaries, builds a multi-platform OCI archive with Debian 13 trixie-slim, and never pushes to a registry. `CONTAINER_ENGINE=docker` uses buildx; default uses existing rootless Podman/QEMU and skopeo local storage. Both architecture images retain Bash/Python/git/CA workflow tooling. The source Dockerfile's Go builder and runtime are also pinned by multi-platform digest. Native client tests still establish compatibility; static linking is not a blanket OS claim.

## Native repositories and release handoff

Sibling site checkout `repo-tools/` contains test-key-only metadata generation, signed manifest checks, a loopback fixture origin and serial distro tests. Five shared packages serve all approved phase-1 roots; no distro-specific builds by default. Debian/Ubuntu use APT metadata; Fedora uses signed RPM/repomd; Arch and Omarchy use identical signed packages/databases. No fake rolling release number. Omarchy VM compatibility remains a separate approval/test gate.

Production sequence after separate authorization: verify encrypted off-machine signing/deploy-key backup, validate unsigned candidate, sign on p15, re-test signed bytes, transfer a signed public release manifest/bundle to m720q, verify there, then publish with its package-restricted key. No private signing keys on uploader or CI. Packages/hash metadata first; mutable indexes/signatures next; APT InRelease last. Keep every version and previous referenced data, fail closed on native signature races and preserve hashes/signature history. Native archive and image-signing keys are distinct; no real-key integration is enabled by the fixture helper.

Before production: approved site-content/repos isolation and root routing/static-only enforcement; two dedicated restricted SSH keys; pinned host trust; staging acceptance; encrypted backup/restore (m720q 8 TB, migrate under CC-48 / inventory CC-70); publication approval. The current personal keys are not automation credentials. No system install or VM provisioning is performed by these scripts.
