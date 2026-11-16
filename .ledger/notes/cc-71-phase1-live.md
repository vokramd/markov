# CC-71 phase 1 live

Human approved publication and the separately reviewed repository-only header correction. All phase-1 services are live and verified:

- Install guide: https://markovd.com/install/
- Release: https://github.com/vokramd/markov/releases/tag/v0.2.0
- Image: ghcr.io/vokramd/markov:0.2.0, OCI index sha256:af3d39587ab56c367f8366e7e50ae37ee893f0cb76d9266d6ceb2a0076f79b51
- Public certificate: https://markovd.com/repos/keys/markov-archive.asc
- Native roots: /repos/debian/13/, /repos/ubuntu/{24.04,26.04}/, /repos/fedora/{44,45}/, /repos/{arch,omarchy}/rolling/ on markovd.com.

Engine tag remains v0.2.0 at 3cf7f1f99c5c5588c66851c32efa0051f592b2ae. Primary fingerprint F50B9360C2C9C876476017A33B8A1C64696AECEF; signing subkey B0BEFAA736416BC0118E5EC33CF4648EB2925C50. Backup was refreshed/retrieved/restored after the human GUI passphrase change; identity/certificate/signatures unchanged. No private signing key entered CI, containers or publication.

Publication: ordered native history/payload/index/InRelease passes verified all 214 public files. Eleven sequential public package rows passed signature/key/architecture checks, installation, exact version, quickstart, update check, removal and preserved user data. Arm container rows used existing QEMU. One-version public update checks are not cross-version upgrades; earlier fixture upgrade evidence remains separate. Existing Omarchy 4.5.0 VM passed supported updater/fixture-to-public package upgrade, fresh installation/removal and unprivileged user-data checks; powered off afterward. Console shell-readiness warning remains, with no new graphical/reboot/snapshot claim.

GHCR anonymous pulls and host-user quickstarts passed for amd64 and arm64 (arm64 emulated). The initial arm64 non-root check failed due to our restrictive local import umask; isolated normal-permission extraction passed and only our defective local import was removed. No registry image rebuild or digest change. No latest tag created.

Draft Release 446472201 received exactly 26 curated assets/certificate; all API hashes/sizes matched. After publication, the verifier initially used draft-only untagged download URLs and got 404. A GET confirmed the same complete published Release and refreshed v0.2.0 URLs; no re-upload or duplicate publication. All 26 anonymous downloads and fresh public-only OpenPGP signature/digest checks then passed. Always refresh asset URLs after publishing a draft.

DreamHost placement is /home/tannerjc/markovd.com/repos/. Human confirmed ample account quota; filesystem/inodes were sufficient. Initial metadata inherited max-age=172800; user specifically approved the tested trusted repos/.htaccess exception. PR https://github.com/vokramd/markovd.com/pull/4 merged at 9de6291c4a2a0a27c17000bd93205ceb2816d3ee. Config SHA256 77d5925dd263704ec01d790f839bbbada63b14c8a1b9d07c06ff570dfaf3ef3f. All 106 native files now revalidate with no Expires, correct metadata MIME and unchanged signed bytes; HEAD/Range/ETag/304 checks passed. This operator file is not accepted from inboxes; dotfile/config validation remains strict.

Only the approved install/index.html, one-link index.html and sitemap.xml were deployed from clean reviewed commit 29eab6307dbe162462d66de6260d559d5397ac95, manifest a32d452ae204d737a9819039485424615085ff7cab1c4134b90c859b60732a13. Canonical /, /install/ and explicit file URLs match approved bytes. Unrelated styles/app/robots/root .htaccess and authorized_keys hashes unchanged. No DNS/account settings, key pruning, official distro submission or outbound messages.

Exact per-file/asset digests, URL results and coverage: cc-71-publication-progress.json; private operational logs/retained artifacts: ~/.cache/cc71/v0.2.0. Keep all versions/history/candidates; no automatic pruning. All build/cache/temp output is on home disk. A final maintainer CLI audit fixed the remaining home-scratch guards locally (site branch cc-71-home-fixture-validation, 98aca53); 66 repository-tool tests passed. Closeout includes this maintainer-only follow-up; it changes no deployed site or signed release bytes.

Phase 2 distro expansion and later official-distro admission work remain deferred; pending tasks preserve their scope. This completes the approved phase-1 launch, not every future phase of CC-71.
