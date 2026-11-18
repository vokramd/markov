# CC-71 Alpine production signing and candidate integration

Pending coding/review after the completed format proof. Prepare the host-only encrypted-PEM native signer, prompt handling, approval/backup receipts, source/request hash validation, independent public-key verification, package/index signatures and final archive-signed manifest. Retain the fixture-only guard in apk-fixture.py. Extend candidate curation/provenance/checksum/SBOM/archival and negative tests for seven native artifacts; then build/validate a clean unsigned CI candidate when outward CI work is authorized.

The local engine config now includes unsigned APK output, but the site release signer intentionally rejects APK-bearing candidates; do not merge that release configuration until the production integration is complete. Existing five-artifact candidates remain accepted. See site repo-tools/PHASE2-ALPINE-REVIEW.md for concrete custody, key-bootstrap and rotation recommendations.

Coding/tests with disposable keys can proceed locally. Generating/backing up the real native key, touching m720q, real signing, source pushes/PRs and any publication still require explicit approval. Do not turn a coding task into a key-creation action. Separate encrypted native RSA-4096 custody on p15 is recommended, not yet approved or created.
