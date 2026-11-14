# CC-71 phase 0 and pilot evidence

2026-11-14, p15. Plan/index committed first: `4588789`, branch `cc-71-packaging-pilot`; sibling site branch has same name. Unrelated `examples/quickstart-report.md` left untouched.

## Read-only DreamHost findings

Used existing desktop agent, `ssh -F /dev/null`, BatchMode, StrictHostKeyChecking=yes, UpdateHostKeys=no, ED25519 only; existing known_hosts fingerprint `SHA256:WNPenzow9Z0aI0IhsOk260VNf8qtz0qhgCHZ591cqLk`. No new host accepted. `hostname` confirms `pdx1-shared-a1-46`; site and tannerjc.net are siblings under `/home/tannerjc`. Existing `.dh-diag` symlink is root-owned; its existence does not prove new symlink behavior or isolation. Site root has static files and `.htaccess`; no repos/site-content yet. `/usr/bin/rrsync -help` confirms `-wo`, `-no-del`, `-munge`, `-no-lock`. No remote files created/modified.

Public HEAD checks: `/` 200 text/html, max-age=0, CSP/security headers, ETag, Last-Modified and Accept-Ranges; `/styles.css` 200 text/css max-age=3600; `/repos/` 404 (no HTML success fallback); `/.htaccess` 403. Apache headers agree with root config. No package MIME/cache behavior can be established on nonexistent paths.

Recommendation: sibling real directories site-content and repos; deploy keys confined separately; root routing maintained by administrator. `Alias` requires server/vhost control, not assumed available in .htaccess. Root config currently has no rewrite layout; site-content migration, static-only execution restrictions, symlink escape and rrsync confinement require separately approved staging writes. Restricted keys cannot switch remote symlinks: packages-first/indexes-last is primary. Coordinator supplied existing m720q access findings; no connection to m720q made.

Sandbox initially blocked SSH config/network/Podman runtime; authorized commands ran with reviewed escalation. No system package installation.

## Pilot validation

Results below are local-only. Do not treat throwaway-key evidence as verification of the real archive key or production publication.

### Completed checks

- Core `make lint test` passed with Go 1.25.13 and temporary caches; `/tmp/cc71-core-tests.log`. GoReleaser 2.20.2 configuration validated; Syft 1.57.1 and checksum-verified bootstrap tools used. Both Linux executables are statically linked ELF (amd64/arm64).
- Eleven clean-container rows passed install, --version, validate/quickstart, 0.1.0 → 0.1.1 upgrade, repeat quickstart and removal while retaining user data. Full matrix and pinned image digests: [JSON results](cc-71-pilot-results.json). Arm64 rows ran under existing QEMU/binfmt, not native arm CPU. Only one heavy matrix row ran at a time, enforced by a file lock.
- APT, dnf and pacman each rejected a bad native metadata signature and corrupted package bytes. Logs: `/tmp/cc71-fixture-v2/negative-{apt,rpm,pacman}.log`. Negative tests explicitly select amd64; cached arm64 images otherwise select an untouched architecture package. The first failed negative run correctly exposed that harness mistake; corrected runs all passed.
- Twelve promotion safety tests passed: signed manifest, full fingerprint, incorrect identity, tampered signature/hash, path traversal/newline, source symlink, unlisted content, phase spoofing, immutable conflict, bytes changed after validation, commit-file ordering. Five deploy-script tests passed, including real rsync parsing through a fake SSH executable with spaces/quotes in a dummy identity path. No network used by deploy tests.
- APT SHA256/SHA512 by-hash verified; signed package bytes match across Debian/Ubuntu and Fedora roots. No Valid-Until. Pacman database aliases are regular files, compatible with proposed link-munging restricted uploads. Ordered upload plans are files only: payloads, indexes, then APT InRelease. No remote uploader is enabled.
- Final unsigned candidate is in `markov/dist/`: five native packages, Linux/macOS archives/source tarball, four archive SPDX SBOMs, per-platform OCI SPDX SBOMs, candidate.json/checksums.txt. Notices for 86 linked modules across all four targets (85 on Linux) are bundled; automated inventory reports no missing root notice files. This is not a completed legal/license audit. Operational ledger/research are excluded from source archives, Docker contexts use an allowlist.
- Local two-platform OCI archive generated without registry publication. Both platforms ran version/quickstart with caller UID/GID and writable work mounts, checked notices; final candidate checks recheck payload hashes and checksum manifest. OCI export uses Podman manifest-to-filesystem transport because skopeo could not resolve child images directly from Podman's list store. No private key or remote signing involved.

### Bugs found/fixed during pilot

Ubuntu slim images omit `/usr/share/doc`, so runnable examples moved to `/usr/share/markov/examples/quickstart.yaml` in all formats/images. Early fixture snapshots were generated new-before-old, causing APT to retain newer-dated old metadata; generator now enforces chronological fixture creation. Added SHA512 by-hash objects for apt's preferred digest. Reset the shell command cache before checking removal. Arch image's nobody login is expired; use an unprivileged container UID directly and restore temp ownership, rather than PAM/su. Fix SSH identity quoting using rsync's parser rules, verified with a fake endpoint. All were local pre-publication defects.

### Evidence limits and gates

Fixtures use version overrides on current engine source, not actual historical release code; they prove native package-manager upgrades, not checkpoint compatibility across historical releases. Final notice-file/build-metadata changes received payload inspection and focused package/OCI validation; matrix snapshots are kept separate in the JSON evidence. Candidates are unsigned local snapshots, not approved clean-tag production releases. The prepared native-arm64 Actions job has not run; no push/dispatch is authorized. Future production requires clean-tag CI evidence, native-arm execution, manual license/security review and the existing publication gates.

No Omarchy VM created. Next request: approve official Omarchy 4.5.0 x86 ISO (as listed at https://omarchy.org/ on 2026-11-14), checksum/signature verification, QEMU/KVM on p15, 4 vCPU, 8 GiB RAM, 64 GiB sparse disk under `/home/jtanner/workspace/github/vokramd/.cc71-vms/`. Existing qemu/virt-install/KVM availability checked read-only; no system packages installed. Guest uses user-mode NAT, no physical disks/agent/home/signing-key mounts. See pending task.

Deploy-key generation, all DreamHost writes/staging/layout/authorized_keys changes, m720q access, real-key use/backup/copy, VMs and outward push/tags/PRs/Releases/GHCR remain explicitly gated. No such action performed. Unrelated `examples/quickstart-report.md` remains untracked and untouched.
