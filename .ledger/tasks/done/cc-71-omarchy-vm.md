# CC-71: Omarchy VM compatibility validation

Status: done for the authorized local VM; approved and completed on 2026-11-14. [VM evidence/limits](../../notes/cc-71-omarchy-vm.md), [structured results](../../notes/cc-71-omarchy-vm.json). Production publication remains gated. Console-only desktop-shell readiness warnings are recorded; GUI QA is not claimed.

Approved and tested VM: official **Omarchy 4.5.0 x86 ISO**, listed at [Omarchy's site](https://omarchy.org/) on 2026-11-14; verify publisher checksum/signature before use. QEMU/KVM on **p15**, 4 vCPU, 8 GiB RAM, 64 GiB sparse qcow2 disk under `/home/jtanner/workspace/github/vokramd/.cc71-vms/`. Existing QEMU/virt-install and accessible KVM checked read-only. VM created with existing OVMF firmware, now powered off. No host packages installed. Private images/helpers/logs remain outside the Git repositories.

Use user-mode NAT; no host agent, user home, physical disks or private signing keys mounted in guest. Connect only the loopback fixture via guest NAT host address or a copied public bundle. Test with disposable fixture trust, never production key. Guest installer/updater may require network; no personal AI/model credentials or external workflow side effects.

Acceptance completed: recorded ISO digest/version/kernel/pacman/repo snapshot; set up `/repos/omarchy/rolling/` fixture; install, version/quickstart, supported system updater, reboot, custom-repository persistence, package upgrade/removal and user-data preservation passed. Native snapshot boot, restore and normal-root reboot with matching old binary/package database passed. Test logs and evidence limits recorded. Treat any newer ISO/version as an explicit documented baseline change, not a fabricated versioned rolling URL.

Approval does not permit DreamHost staging/writes or authorized_keys changes, deploy-key generation, touching m720q, copying/signing with the real key or public releases/pushes. Those remain separate gates.

Execution: QEMU 10.2.2, qemu-img and non-secure OVMF firmware are already installed. No host package installation needed. VM approval covers ISO download/verification and local guest setup; other publication/credential gates remain unchanged.
