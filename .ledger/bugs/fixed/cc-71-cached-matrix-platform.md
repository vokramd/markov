# Podman reused a cached opposite-architecture matrix image

During v0.2.2 public acceptance, Tumbleweed x86_64 stopped before installation:
Podman warned that the cached image was Arm64 even with `--platform=linux/amd64`.
Zypper verified Markov metadata, then correctly found no compatible x86_64
package for that Arm userspace. This is a test-runner cache-selection bug, not
a package signature or repository metadata failure. The Release remained draft.

Audited all 25 completed client logs: none had a platform mismatch warning,
and their package selection/execution checks passed. Preserve those results.
The failed log was retained separately before a clean continuation.

Site commit `2d679fb` adds `matrix_image.select_image`: inspect the actual cached
OS/architecture, explicitly pull the pinned reference for the requested platform
if necessary, reject a remaining mismatch, and run by the checked immutable
image ID. Applied to positive and negative fixture runners and the public
acceptance continuation. Four focused regressions plus existing safety tests
passed: 111 total. No signed artifacts changed and no real-key re-signing occurs.

Both Tumbleweed architectures passed with checked immutable image IDs, followed
by both Alpine architectures; all 29 public rows are now green. The old rolling
index was also unavailable upstream (`manifest unknown`), so the two Tumbleweed
rows were refreshed to official index `sha256:1f02d7bb849db90544a1f868b8f07ed97f8150ad64f6ae96a690ba1bb2c71b9f`
(site commit `97d8b1a`). No completed valid row was rerun. The final Release, page
and public verification subsequently completed.
Evidence: `~/.cache/cc71/v0.2.2/stable/publication/`, initial and resumed finish
logs, retained failed client log, and `native-public-results.json`.
