# Publish v0.2.2 expanded first-party repositories

Current: merges and publication authorized, both PRs merged, clean v0.2.2 tag/candidate/native-Arm CI complete. See [prepared checkpoint](../../notes/release-v0.2.2-prepared.md) and JSON receipt. Candidate is unsigned and prepared, not published.

Waiting for human availability at p15: new dedicated native RSA4096 identity with confirmed password, independently encrypted off-machine backup plus restore, then native and archive signing prompts. Availability question pending. Do not use test keys or an unverified backup receipt. No production identity exists yet.

Then: independently review source/CI/native identity approval; sign and validate real candidate; clean install/update/remove/negative tests for agreed matrix; freeze install page with actual native hash/digest; ordered native repository/Release/GHCR publication and anonymous verification. Keep every old version and existing v0.2.1 endpoints until promotion. No markovd/harness packaging or official distro submission.
