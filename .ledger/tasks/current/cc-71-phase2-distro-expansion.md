# CC-71 phase 2: distro expansion

Local acceptance complete for the proposed 18 new container rows: [enterprise RPM](../done/cc-71-phase2-rpm-pilot.md), [openSUSE](../done/cc-71-phase2-opensuse-pilot.md) and [Alpine format proof](../done/cc-71-phase2-alpine-format-proof.md). x86_64 ran natively and aarch64 under existing QEMU; no new native Arm CI evidence. All work is committed locally, not pushed or published.

[Alpine native signing/candidate integration](../done/cc-71-alpine-production-signing.md) is now locally implemented and tested with encrypted throwaway keys. Remaining: [source/CI and production review](../pending/cc-71-phase2-release-review.md). Real-key creation/backup/signing, source pushes/PRs, new public roots and live installation-page changes remain gated. Official distro admission remains separate. Follow [plan 013](../../plans/013-cc-71-phase2-distro-expansion.md).
