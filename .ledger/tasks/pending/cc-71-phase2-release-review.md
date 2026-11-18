# CC-71 phase-2 source/CI and production rollout review

Pending external review/authorization after local implementation. Engine branch `docs/cc-71-phase2-plan` contains distro plan/ledger and unsigned APK output with r0 override. Site branch `feat/cc-71-phase2-rpm` contains expanded repositories/matrix, host-only native signing and local evidence. See site `repo-tools/HOST-NATIVE-SIGNING.md` for exact custody/backup approval additions and remaining gates.

Next reviewable outward step: authorize source pushes/draft PRs and unsigned hosted/native-Arm CI. That does not need the human physically at p15. A clean tag/candidate for actual publication still needs a selected release version and separate approval; local 0.2.2 fixture is not a release proposal.

Human-present steps, separately approved: create dedicated encrypted native RSA4096 key; verify local signing; independently encrypt/copy backup to the confirmed m720q destination and test restore; approve identity/backup receipts and source/CI candidate; perform real signing. New public key/repository roots, installation-page deployment and all outward publication remain gated. No official-distro submission is included.
