# CC-71 phase 2: extend first-party Linux repositories

Status: first-party implementation and publication complete through v0.2.2 (2026-11-19). [Live closeout](../notes/release-v0.2.2-live.md), [exact evidence](../notes/release-v0.2.2-live.json), and [merged reviews/approved password cleanup](../notes/release-v0.2.2-merged-and-cleanup.md) supersede the historical preparation gates below. Official distro admission research remains separate.

## Reconciled current state — 2026-11-19

- Scope: Markov engine only. First-party phases 1 and 2 are live; no daemon/harness packaging or official-distro endorsement.
- Seven shared native artifacts (2 deb, 2 rpm, 1 pacman, 2 apk), 29 public container rows, additional Omarchy-route pacman upgrade and earlier recorded Omarchy VM evidence. No new v0.2.2 Omarchy VM claim.
- RHEL/UBI, Rocky, AlmaLinux 9/10; Leap 16.1 and Tumbleweed `rolling`; Alpine 3.24 are implemented, not deferred candidates. Current matrix is site `repo-tools/matrix.json`.
- CI builds unsigned candidates; p15 signs with the dedicated KeePassXC vault and manually publishes via pinned admin SSH/GitHub REST/GHCR. Restricted deploy keys target non-web inboxes only; m720q is not the current release publisher.
- Encrypted vault/native-key backup on mounted m720q `/mnt/wdc_16tb/BACKUPS/` was retrieved and restore/sign verified; archive/deploy backup history is preserved under CC-48/CC-70. Original unpublished Alpine identity retained. Old plaintext GPG password source removed after explicit approval.
- GHCR authenticity uses the GPG-signed release manifest/checksums covering the exact OCI index digest. A separate Cosign key/signature/attestation scheme was proposed but is not deployed; it remains optional future hardening.
- Historical versions retained. Signing and live publication still require authorization for each future release; prior releases do not grant standing approval for new outward actions.
- Remaining: official distro readiness/possible admission implementation; routine maintenance/rolling checks; optional host isolation and registry-native signatures. Admission research is complete in [report 014](014-cc-71-distro-admission-research.md). The original design and dated checkpoints below are preserved as history where they differ from this implemented state.


## Original recommended decisions (implemented; historical baseline)

1. Validate the existing signed RPMs on RHEL (using UBI), Rocky and Alma first; do not rebuild per distro.
2. Cover majors 9 and 10, latest supported minor only, on x86_64 and aarch64. Gate each image on its actual CPU requirements.
3. Use Leap 16.1 and Tumbleweed rolling: release/lifecycle and both image architectures are verified. Do not add 16.0 implicitly.
4. Add Alpine 3.24 after proving native package/index format and signing compatibility with the pinned tools.
5. Keep manual p15 signing/promotion, existing GitHub unsigned candidates, and all historical versions. No additional markovd service is needed.
6. Review support evidence and installation text before publishing any new root. Official distro admission remains a separate later task.

## Existing implementation and ownership (pre-expansion baseline)

The engine repository owns `.goreleaser.yaml`, `packaging/arch/`, the Dockerfile and release workflows. Current artifacts are two debs, two RPMs and one x86_64 pacman package. The Linux binaries use CGO_ENABLED=0, GOAMD64=v1 and GOARM64=v8.0; the existing release evidence confirms static ELF files. Native packages still require bash and CA certificates; optional workflow programs remain user-installed dependencies. A static executable does not establish package-manager compatibility by itself.

The site repository owns `repo-tools/` and `/install/`. Its actual index pipeline uses dpkg-scanpackages/apt-ftparchive, createrepo_c and repo-add, rather than the earlier aptly proposal. `unsigned-indexes.py`, `indexes.py`, `release-bundle.py` and `inbox.py` currently encode the phase-1 roots and formats; expand these explicit allowlists and their rejection tests together. Preserve the existing packages/history-first, indexes-next, InRelease-last publisher and fail-closed manifest validation.

GoReleaser v2.20.2 and Syft v1.57.1 are already pinned; actions are pinned by SHA. Reuse this workflow and its native Arm checks. Verify the embedded nFPM version before relying on additional APK features. CI gets no signing or publishing secrets. GHCR remains the existing two-architecture image on digest-pinned trixie-slim; adding distro roots does not require additional images. Scope remains `markov` only, excluding markovd and harness packages.

## Approved support matrix (implemented in v0.2.2)

All rows passed public acceptance in v0.2.2; the candidate-era rationale below is historical. Major/minor roots below are stable interfaces, not claims of upstream endorsement.

| Sequence | Distro/root version | Architectures | Package source | Added smoke rows |
|---|---|---|---|---:|
| A | rhel/9, rhel/10 (UBI validation) | x86_64, aarch64 | Existing RPM per arch | 4 |
| A | rocky/9, rocky/10 | x86_64, aarch64 | Existing RPM per arch | 4 |
| A | almalinux/9, almalinux/10 | x86_64, aarch64 | Same RPMs | 4 |
| B | opensuse/16.1 | x86_64, aarch64 | Same RPMs, if compatible | 2 |
| B | opensuse/rolling, Tumbleweed | x86_64, aarch64 | Same RPMs, if compatible | 2 |
| C | alpine/3.24 | x86_64, aarch64 | Two new native APKs from existing binaries | 2 |

Full proposal adds 18 container rows to the existing 11; Omarchy retains its separate VM acceptance. Native package count becomes seven, not one build per row. If Leap 16.0 is also requested and maintained, it adds two validation rows, not new RPMs. Do not implicitly add it alongside 16.1. No EL8, Alpine edge, i686, ARMv7, ppc64le, s390x or riscv64 support is proposed.

Research checked 2026-11-17:

- Human addition: include RHEL 9 and 10 using freely available UBI images. [Red Hat documents](https://access.redhat.com/articles/4238681) unauthenticated access through registry.access.redhat.com and subscription-free UBI repositories containing a subset of RHEL RPMs. Use the standard `registry.access.redhat.com/ubi9/ubi` and `registry.access.redhat.com/ubi10/ubi` images as candidate inputs; resolve and pin their actual per-platform digests before testing. Verify bash, CA certificates and DNF trust options using only UBI repositories plus the Markov fixture repository, with no subscription credentials or host entitlement mounts. Report these rows as “RHEL 9/10 — UBI container validated”; they establish userspace/package compatibility, not full RHEL host, SELinux, FIPS, certification or Red Hat support coverage. Record CPU baselines and available manifests for both architectures before accepting the rows.

- [Rocky release guide](https://docs.rockylinux.org/releases/) lists 9.9/10.3 and supports the latest minor in each major. Rocky 9 requires x86-64-v2; Rocky 10 requires v3. Major 9 ends support in 2032 and 10 in 2035. Test host/runner CPU and official image availability before scheduling those rows; a GOAMD64=v1 executable does not lower the operating system baseline.
- [Alma release notes](https://wiki.almalinux.org/release-notes/) list 9.9/10.3, with major support through 2032/2035 and latest-minor transitions. Alma 10 also lists an x86_64_v2 variant: select and record the exact image/variant rather than assuming every x86_64 image has the same baseline.
- Resolved in phase B: the [official announcement](https://news.opensuse.org/2026/11/03/leap-unites-traditional-and-immutable) confirms Leap 16.1 released 2026-11-03. Its [release notes](https://doc.opensuse.org/release-notes/x86_64/openSUSE/Leap/16.1/html/release-notes-leap-161/index.html) specify 24 months of community maintenance per minor. Official Leap 16.1 and Tumbleweed indexes expose amd64/arm64 manifests and are pinned in the site matrix. This supersedes plan 012's unverified 16.0 selection. Record Tumbleweed's actual VERSION_ID and image digest, keeping the public version token rolling.
- [Alpine's branch table](https://www.alpinelinux.org/releases/) currently lists 3.24.4, with 3.24 main/community support and branch end 2028-06-01; community support changes at the next stable release. Recheck before implementation. Do not preselect an unverified future 3.25 release.
- [nFPM's APK configuration](https://nfpm.goreleaser.com/docs/configuration/) uses an RSA PEM signing key and a public key filename under `/etc/apk/keys`; it does not use OpenPGP. This does not prove the pinned producer's package/index format is accepted by the selected Alpine client. Establish that experimentally before finalizing C.

## URL and native metadata contract

Proposed RPM roots are exactly:

```
https://markovd.com/repos/rhel/9/stable/<arch>/
https://markovd.com/repos/rhel/10/stable/<arch>/
https://markovd.com/repos/rocky/9/stable/<arch>/
https://markovd.com/repos/rocky/10/stable/<arch>/
https://markovd.com/repos/almalinux/9/stable/<arch>/
https://markovd.com/repos/almalinux/10/stable/<arch>/
https://markovd.com/repos/opensuse/16.1/stable/<arch>/
https://markovd.com/repos/opensuse/rolling/stable/<arch>/
```

`<arch>` is x86_64 or aarch64. Each root contains packages and native `repodata/repomd.xml` plus its detached signature. The signed RPM bytes must hash identically across compatible roots; root-specific metadata may differ. Native clients select architecture through `$basearch`, not HTTP redirects. EL paths use the major, never the full minor from `$releasever`; installation instructions must normalize it explicitly. openSUSE `rolling` means Tumbleweed, not Leap or a numeric alias. These are new proposed roots; no existing public URL is renamed.

DNF configuration template, instantiated with an approved distro/major:

```ini
[markov]
name=Markov first-party packages
baseurl=https://markovd.com/repos/rocky/9/stable/$basearch/
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=https://markovd.com/repos/keys/markov-archive.asc
```

The archive-key URL matches the current install-page source; verify its fingerprint before trusting it. openSUSE gets a tested zypper `.repo` example with both package and repository verification enforced; do not assume DNF-specific options alone enforce zypper policy. Document fingerprint verification before trust, then install/refresh/update/remove commands for each client.

For Alpine, propose `https://markovd.com/repos/alpine/3.24/stable/<arch>/` with native APK metadata. The local nFPM v2.49.0 / apk-tools 3.0.9 proof accepts APK v2 and `APKINDEX.tar.gz` with RSA256 native signatures on both architectures; this resolves the format gate. The host-only encrypted native signing/candidate integration now passes local disposable-key acceptance; real native identity and rollout review remain pending. Alpine's repositories file normally takes the root without the architecture suffix; test automatic architecture selection. Publish a separately named native public key and verification digest only after human approval. Never use `--allow-untrusted` in installation instructions.

Use only `stable` initially. Any future `testing` channel requires a separately validated root and explicit opt-in; no alias silently follows prereleases. Existing apt, Arch and Omarchy URLs remain unchanged. Keep apt Valid-Until deferred as decided in phase 1.

## Work phases and estimates

A, about 3–4 engineering days after images are obtainable: enumerate digest-pinned official images and runner CPU requirements; extend RPM root generation/allowlists; run twelve clean rows using the existing RPM bytes. Check older RPM readers, compression/signature compatibility, dependency names, repository signature enforcement and version ordering. If byte reuse fails, present the concrete incompatibility before introducing a format variant. Produce a local signed fixture using only a throwaway key and an installation-doc draft.

B, about 1–2 days after lifecycle/image evidence: choose the maintained Leap version; add its two rows and two Tumbleweed rows. Validate zypper repository and package trust, snapshot churn and architecture mapping. Rolling support is best-effort; keep the previous passing snapshot digest as evidence, without instructing users to pin their entire OS to it.

C, about 2–4 days plus key/backup review: prove pinned nFPM unsigned APK output and native index generation against Alpine 3.24 on both architectures. Prefer reuse of the exact Linux binary. If nFPM cannot produce the required format, assess a pinned abuild wrapper before adding a second build chain. Prepare a host-only native-signing adapter; do not expose a real PEM private key to CI or fixture containers. New key generation, backup, real signing and public key publication require separate approval. Add the two rows and native negative tests before proposing publication.

Final review/promotion, about one day excluding human signing time: collect per-row evidence, update install page/runbook and review all new roots together. These are effort estimates, not delivery promises. Run only one heavy local matrix test at a time. Set GOCACHE, GOMODCACHE, TMPDIR and fixture outputs on the home disk via the CC-71 environment; never use shared `/tmp` for large builds. GitHub Actions is sufficient for release validation; future markovd scheduling can orchestrate checks but is not a prerequisite.

## Acceptance, custody and operations

For every row: pin image digest/platform, record CPU/client versions, fetch via a local HTTP fixture origin, enforce native trust, install an older fixture version, run `--version` and quickstart, upgrade through the repository, rerun quickstart, remove, and check user data survives. Test bash/CA dependencies and correct architecture selection. Distinguish native Arm CI evidence from emulation. Add missing/wrong-key, modified package/index/hash, expired/revoked test-key where supported, stale index and interrupted upload tests. A negative test passes only when installation fails for the intended trust/integrity reason.

Existing signed historical artifacts establish RPM upgrades without rewriting released bytes. For new APK format, create two fixture versions from clean recorded sources; do not pretend there was a previously published Alpine release. Verify package contents, license/source metadata, SBOM/checksums and absence of credentials/runtime state. No service units are needed for the CLI.

Keep the real archive OpenPGP key on p15, signing with human presence. A new Alpine key must have separate native identity, recovery instructions and encrypted backup on m720q's `/mnt/seagate_8tb` destination with its CC-48 migration to 16 TB tracked alongside CC-70. Confirm the current approved backup location before copying anything. Use encryption independent of the keyring-adjacent passphrase and test restore before any public key/package publication. Do not export or convert the OpenPGP private key as a shortcut.

Promotion remains manual on p15 over existing admin SSH. Restricted deploy keys, when used, write only to non-web inboxes; p15 snapshots/validates uploads and publishes approved regular files. No server-side promoter or additional shared-host installation is needed. Packages and immutable history go first, native indexes next, apt InRelease last. Signatures/checksums must describe the final signed bytes. Keep all versions; check disk/quota before every release. Current native metadata uses revalidation cache policy; do not claim package URLs already have immutable cache headers.

Retain the previous complete signed metadata snapshot for rollback. Restore metadata only to packages already present, test explicit downgrade separately, and never delete historical bytes or re-sign an old version in place. Keep package/index upload retries idempotent and fail closed on incomplete manifests. Extend allowlist/traversal/symlink/hardlink/config/script/race rejection tests for new formats without weakening them. Rotation needs documented overlap, trust-bootstrap and revocation steps for both OpenPGP and any Alpine-native key.

## Historical rollout gates and subsequent work

Approve the proposed major/version/architecture matrix and the A→B→C order. Resolve Leap lifecycle/version and Alma 10 image variant in the first implementation research checkpoint. Approve native Alpine key custody/backup only after format proof. New public roots, real signing/public keys, tags, releases, GHCR changes and deployment remain separately gated; this plan authorizes none of them.

After first-party expansion, resume [official distro admission research](../tasks/done/cc-71-distro-admission-research.md): verify Fedora review/sponsorship and Go bundling rules, Debian ITP/sponsor then Ubuntu syncing, and Omarchy acceptance versus Arch official/AUR. Audit `third_party/pongo2`, license/source completeness, reproducible release tarballs, man page and maintainer capacity first. No first-party root or PPA is described as an official distro repository, and no submission/contact is part of this plan.

## Admission research checkpoint — 2026-11-19

[Report 014](014-cc-71-distro-admission-research.md) completes the separately assigned policy/gap research. Fedora now requires vendored dependencies for new Go packages; Debian follows its own source/dependency policy. Initial local prototype/readiness tasks are proposed, not implementation or submission authorizations. Source/publications remain unchanged.
