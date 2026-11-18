# CC-71 phase C: Alpine native format/trust proof

Completed local proof 2026-11-17. Alpine 3.24.4 with apk-tools 3.0.9 accepts nFPM v2.49.0 APK v2 packages and native RSA256 package/index signatures. Two rows passed install/upgrade/removal/version/quickstart/user-data preservation; two rows rejected changed index signatures and authenticated payload corruption. Reused binaries independently checked inside APK payloads. Final site suite: 76 tests passed.

Site commit `7f7241f` on `feat/cc-71-phase2-rpm`; review/evidence `repo-tools/PHASE2-ALPINE-REVIEW.md` and `PHASE2-ALPINE-RESULTS.json`. Engine unsigned APK configuration passes GoReleaser check; no new clean CI APK candidate built. Production candidate signing explicitly rejects APKs until native integration is ready. This is a fixture compatibility proof, not production-ready APK support. No real/native production key, backup, publication or pushes. Local server stopped.
