# CC-71 phase A: enterprise RPM local pilot

Completed 2026-11-17. RHEL/UBI 9/10, Rocky 9/10 and AlmaLinux 9/10 each passed x86_64 and aarch64 install, upgrade 0.2.0→0.2.1, version, quickstart, removal and preserved-user-data checks: 12 rows. Each also rejected damaged metadata signatures and RPM hashes. All 69 repository-tool tests passed. Arm64 used existing QEMU on p15; this is not native Arm CI or full RHEL host validation.

Implementation: site branch `feat/cc-71-phase2-rpm`, commit `be4951f`. Review/evidence: site repository `repo-tools/PHASE2-RPM-REVIEW.md` and `PHASE2-RPM-RESULTS.json`. Image indexes pinned, original 178-file bundles verified, signed fixture RPM bytes identical across roots. Only a new marked throwaway test key was used. Home-disk fixture logs: `~/.cache/cc71/fixtures/phase2-rpm/`. Loopback server stopped after acceptance.

No pushes, public roots, live-page changes, real-key signing or publication. Review source/evidence before outward work. openSUSE/Alpine remain subsequent phases in plan 013.
