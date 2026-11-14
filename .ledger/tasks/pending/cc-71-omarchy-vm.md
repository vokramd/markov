# CC-71: Omarchy VM compatibility validation

Status: pending **explicit human approval to create a VM**. Local pilot completed; [evidence](../../notes/cc-71-phase0-and-pilot.md). Arch container success is not Omarchy validation.

Proposed VM: official **Omarchy 4.5.0 x86 ISO**, listed at [Omarchy's site](https://omarchy.org/) on 2026-11-14; verify publisher checksum/signature before use. QEMU/KVM on **p15**, 4 vCPU, 8 GiB RAM, 64 GiB sparse qcow2 disk under `/home/jtanner/workspace/github/vokramd/.cc71-vms/`. Existing QEMU/virt-install and accessible KVM checked read-only. No VM/image/disk created. Check existing UEFI firmware before creation; installing any missing host package is a separate approval gate.

Use user-mode NAT; no host agent, user home, physical disks or private signing keys mounted in guest. Connect only the loopback fixture via guest NAT host address or a copied public bundle. Test with disposable fixture trust, never production key. Guest installer/updater may require network; no personal AI/model credentials or external workflow side effects.

Acceptance: record ISO digest/version/kernel/pacman/repo snapshot; set up `/repos/omarchy/rolling/` fixture; install, version/quickstart, full system update, reboot, ensure custom-repository persistence, package upgrade/removal and user-data preservation. Record snapshot/restore behavior and test logs. Treat any newer ISO/version as an explicit documented baseline change, not a fabricated versioned rolling URL.

Approval does not permit DreamHost staging/writes or authorized_keys changes, deploy-key generation, touching m720q, copying/signing with the real key or public releases/pushes. Those remain separate gates.
