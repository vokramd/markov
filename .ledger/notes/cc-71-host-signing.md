# CC-71 local host-only signing evidence — 2026-11-15

Implemented the p15 native signing path using only a disposable certification primary and signing subkey. Full five-artifact pipeline completed with 106 inventoried repository files and 23 checksummed curated artifact files. Both RPM architectures passed native signature/digest verification. Metadata containers received public files only, no private key or agent socket, and ran without network. Original unsigned CI candidates are retained in a sibling vault outside container mounts. See the companion JSON for identities, image IDs, manifest hash and OCI digest.

Fresh client tests passed Debian 13 amd64 APT signatures, Fedora 45 x86_64 package plus repository signatures, and Arch rolling x86_64 package plus database signatures: install, version, packaged quickstart and remove. These focused interoperability tests used the earlier full signing fixture; the final end-to-end rerun incorporated filesystem/vault safeguards. They do not replace native Arm CI or claim another complete eleven-row upgrade run. Earlier full matrix and Omarchy VM evidence remains separate.

Final checks: 62 repository-tool tests, five deploy-script tests, five OCI flat/nested layout tests, shell syntax. GPG tests require host agent sockets and passed outside the restricted socket sandbox. Production prepare rejected the current dirty local/no-CI candidate before work creation or key access. All outputs remain fixture-labelled and non-publishable. Pinning is not a claim of independently demonstrated bit-for-bit reproducibility.

Production approval additionally requires independent private human review matching the actual tag, commit, CI run, candidate hash, native Arm execution, artifact download and license review. This is an operator verification gate, not a cryptographic CI attestation. Real release signing remains unperformed. The initial signer supports stable X.Y.Z/revision 1 only.

Next: request approval to push both existing feature branches and open draft PRs for unsigned/native-Arm CI. No tags, merge, release, GHCR or live publication implied. Keep all versions, including unsigned source candidates and signed final artifacts; download CI artifacts before temporary expiry.

## Shared scratch cleanup and PR follow-up

Removed obsolete /tmp/cc71-fixture, cc71-fixture-v2 and cc71-omarchy-fixture. Moved Go cache, Go toolchain/module cache, tools and candidates to ~/.cache/cc71/{go-cache,go,tools,candidates}. Owned read-only module directories required writable directory permissions for removal after verifying the copied remaining files. Other agents' paths were untouched. Post-cleanup /tmp: 16G total, 476M used, 16G available, 3%. Future commands source ~/.cache/cc71/env.sh for GOCACHE, GOMODCACHE, TMPDIR and home-disk fixture outputs. Fixture tooling now permits only dedicated home scratch or legacy /tmp paths; marker and real-key refusal remain intact. 63 repository-tool tests passed with home-disk TMPDIR.

Draft PRs: https://github.com/vokramd/markov/pull/1 and https://github.com/vokramd/markovd.com/pull/1. First candidate CI failed because the optional MARKOV_CANDIDATE_VERSION template used missing-map lookup. Changed to safe index lookup; pinned GoReleaser check and a no-build/no-publication snapshot run with the variable absent passed (0.1.1-dev). Native Arm was skipped behind this failure; follow-up CI must verify it.

## Arch CI ownership correction

Run 43749311397 passed the engine test/lint and unsigned GoReleaser build, then failed after makepkg succeeded: Docker restored container-root ownership and the non-root runner could not remove .PKGINFO. The wrapper now restores host UID/GID for Docker and container-root for rootless Podman. Local rootless Podman packaging/cleanup passed using home-disk TMPDIR. GitHub Docker/native Arm execution remains to be confirmed by the updated PR run.
