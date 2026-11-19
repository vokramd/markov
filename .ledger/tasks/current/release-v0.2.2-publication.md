# Publish v0.2.2 expanded first-party repositories

Current: merges and publication authorized, both PRs merged, clean v0.2.2 tag/candidate/native-Arm CI complete. See [prepared checkpoint](../../notes/release-v0.2.2-prepared.md) and JSON receipt. Candidate is unsigned and prepared, not published.

Human was ready and created the encrypted native identity successfully. [Key setup checkpoint](../../notes/release-v0.2.2-key-setup.md): backup GUI did not complete, so backup/restore verification remains pending. Reopen only the backup dialog after readiness reply; never regenerate/overwrite the existing key or infer a verified backup receipt. Release signing/publication have not begun.

Then: independently review source/CI/native identity approval; sign and validate real candidate; clean install/update/remove/negative tests for agreed matrix; freeze install page with actual native hash/digest; ordered native repository/Release/GHCR publication and anonymous verification. Keep every old version and existing v0.2.1 endpoints until promotion. No markovd/harness packaging or official distro submission.
