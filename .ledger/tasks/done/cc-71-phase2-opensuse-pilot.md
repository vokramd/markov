# CC-71 phase B: openSUSE local pilot

Completed local acceptance 2026-11-17. Official Leap 16.1 release/lifecycle confirmed; official image indexes pinned for Leap 16.1 and Tumbleweed (tested VERSION_ID=20261116). Four rows passed install, upgrade, version, quickstart, removal and user-data preservation. Four native zypper signature/payload rejection rows passed. Both metadata and package verification enforced. x86_64 native, aarch64 QEMU on p15. Same signed RPM bytes across Fedora/openSUSE fixture roots.

Site commit `90bcc4d` on `feat/cc-71-phase2-rpm`; review/evidence `repo-tools/PHASE2-OPENSUSE-REVIEW.md` and `PHASE2-OPENSUSE-RESULTS.json`. Disposable keys only, no production signing or publication. No SLES/immutable host claim. Loopback server stopped.
