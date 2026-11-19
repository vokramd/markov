# CC-71 phase 2: distro expansion

Completed in v0.2.2, published and publicly verified 2026-11-19. RHEL 9/10 (UBI), Rocky 9/10, AlmaLinux 9/10, openSUSE Leap 16.1/Tumbleweed rolling and Alpine 3.24 add 18 container rows. The complete first-party matrix has 29 container rows; seven shared native artifacts, not distro-specific builds.

Both package architectures, native APK signing/recovered backups, candidate/native-Arm CI, public repositories/Release/GHCR/install page and native rejection tests passed. Arm distro clients were emulated; native Arm engine CI passed separately. The additional explicit Omarchy pacman route passed; no new Omarchy VM was claimed. See [live closeout](../../notes/release-v0.2.2-live.md) and [exact evidence](../../notes/release-v0.2.2-live.json). Source PRs #7 and closeout PRs #8 are merged.

Official distro admission remains separate. Routine upkeep and optional hardening are ongoing, not uncompleted first-party publication gates.
