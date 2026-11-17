# Markov v0.2.1 live closeout

Human approved publication after the signed-artifact review. **Published and
verified** on 2026-11-17. Engine only; phase 2 and distro admission remain deferred.

- [Install](https://markovd.com/install/), [Release](https://github.com/vokramd/markov/releases/tag/v0.2.1).
- Native repositories under https://markovd.com/repos/ keep the phase-1
  distro/version/channel/architecture scheme and all older versions/history.
- GHCR :0.2.1 and :latest both resolve anonymously to
  sha256:76ab27b0a8b0e820b5925065267e419cc886f1a63115935d7fad7bec02515610.
- Source fcb6ce0, clean-tag/native Arm CI 44109044662. Archive signing identity
  unchanged; primary F50B9360C2C9C876476017A33B8A1C64696AECEF and subkey
  B0BEFAA736416BC0118E5EC33CF4648EB2925C50. Real signing completed through GUI
  pinentry before publication; no key/credential copied to CI or shared host.

## Live evidence

1. Ordered native promotion verified 108 history, 62 payload, 41 index and
   3 final InRelease paths, then the complete public view against signed hashes.
2. All 11 public container rows installed v0.2.0 from retained signed public
   history, upgraded through current public repos to v0.2.1, ran version and
   quickstart, removed the package and preserved user data. No signature bypass.
3. Both architectures were downloaded anonymously into a fresh OCI archive;
   digest/binary identity and host-user persisted quickstart passed. Arm runtime
   is QEMU-emulated on p15; separate native Arm CI also passed.
4. All 26 Release assets have matching server digests and anonymous downloaded
   hashes. Fresh public-only GPG verification of downloaded final checksums passed.
5. Only install/index.html was promoted; its public bytes match reviewed commit
   982caac. Unrelated site/configuration/authorized_keys baseline hashes unchanged.
6. Representative public key/APT/RPM/pacman MIME/hash/revalidation checks and
   package 206 Range passed. Existing all-revalidation policy retained; no
   .htaccess, DNS, server-side promoter, inbox key or daemon changes.

The credential helper was adapted to open only the designated token path through
its configured symlink, validating the actual descriptor as owner-only 600.
No alternate token search, credential persistence or permission changes. Draft
asset URLs were refreshed after Release publication before anonymous verification;
no repeated Release write. The Range/cache test was aligned with existing approved
all-revalidation policy; no hosting change was used to satisfy a test assumption.

[Structured receipt](release-v0.2.1-live.json) holds all row/asset/digest details.
Private operational receipts, unsigned vault and signed artifacts remain under
~/.cache/cc71/v0.2.1/stable/ . No fresh Omarchy VM or native macOS execution is
claimed; historical Omarchy acceptance remains separate. At the publication checkpoint, source-review branches were committed locally.
The human subsequently authorized source closeout through reviewed PRs; this
document retains the publication-time evidence.

Rollback retains all immutable versions. A new increasing release is preferred
for engine rollback; back up workflow state before downgrade. Previous install
HTML and protected-file hashes are retained in publication/previous-install.html
and unchanged-baseline.sha256. Do not delete published versioned assets/images.
