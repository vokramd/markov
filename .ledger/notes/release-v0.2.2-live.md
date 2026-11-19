# v0.2.2 is public

[Release and 28 downloads](https://github.com/vokramd/markov/releases/tag/v0.2.2),
[install page](https://markovd.com/install/), first-party native repositories and
`ghcr.io/vokramd/markov:0.2.2` / `latest` are live. Publication ran from p15 through
pinned admin SSH, packages/history first, indexes next, `InRelease` last.
All 416 current/history public paths matched their reviewed hashes.

All 29 matrix rows passed against public HTTPS repositories: Debian 13,
Ubuntu 24.04/26.04, Fedora 44/45, Arch rolling, RHEL 9/10 UBI, Rocky 9/10,
AlmaLinux 9/10, openSUSE Leap 16.1/Tumbleweed and Alpine 3.24, on the agreed
architectures. The original 11 rows upgraded from retained signed v0.2.1
history. The newly added 18 rows installed and refreshed current v0.2.2;
two-version upgrade coverage was already proven in local signed fixtures.
All checked version, quickstart, removal and preservation of user data.

An additional Arch userspace container upgraded through the explicit
`https://markovd.com/repos/omarchy/rolling/` URL with required package/database
signatures. This verifies the Omarchy repository route, not a newly run Omarchy
VM. Arm distro execution was emulated on p15; the independent tag CI candidate
and native-arm64 jobs both succeeded. No full RHEL/FIPS certification is claimed.

All 28 anonymous Release downloads matched server/local hashes. A fresh
public-only keyring verified their checksums and manifest with the expected
archive signing subkey. A complete anonymous multi-architecture GHCR pull
passed exact version/binary-hash and persisted quickstart checks on both
architectures. Both version and latest index digests are
`sha256:2e826c5e727b7cd034c732b53ad8a8e2e82891f49a228f363ac806cb7bc18fb5`.

The final page passed six local viewport widths and live mobile/desktop
Playwright checks, all 20 exact keyboard-copy commands, anchors and no-JS
layout. Live page bytes match the frozen reviewed snapshot. Native metadata,
APK/public-key MIME types, no-redirect behavior and cache headers passed.
Only the scoped repository MIME config and approved install HTML changed;
all unrelated site/config/authorized_keys baseline hashes remain unchanged.
v0.2.0/v0.2.1 signed histories, every old versioned native package path and old
GHCR version tags remain accessible. No version was deleted.

Signing used the recovered dedicated KeePassXC vault and separately encrypted,
off-machine restored native identity. The earlier unpublished key and backup
remain preserved. GPG identity is unchanged; the old password source was not
deleted. See [signed review](release-v0.2.2-signed.md) for custody/recovery proof.
No password prompt was needed during final signing/publication after the
process-cache correction. Safety suite: 111 tests passed.

The public matrix caught a [cached-image platform issue](../bugs/fixed/cc-71-cached-matrix-platform.md)
and expired upstream rolling-image pin; both were fixed before continuing.
The [Release transition guards](../bugs/fixed/cc-71-release-draft-transition.md)
also stopped on draft tag/URL state until explicit reconciliation. Failed logs
remain preserved; no failed signing workspace was resumed.

[Exact closeout receipt](release-v0.2.2-live.json). Site/operator source changes
are in [draft PR #8](https://github.com/vokramd/markovd.com/pull/8); the site has
no hosted CI configured, so Python/browser checks were run on p15. Deployment
used reviewed page bytes from commit `a3e463174ee07d2902235fe7e5f4e5a6da76901f`.
Official distribution admission remains future work. No markovd/harness
packages, VM creation, DNS changes or official-repository submission occurred.
