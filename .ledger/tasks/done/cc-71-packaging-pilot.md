# CC-71: local engine packaging pilot

Status: done for the authorized local pilot; CC-71 production acceptance is not complete; subsequent [Omarchy VM validation](cc-71-omarchy-vm.md) is complete with recorded limits. Plan: [012](../../plans/012-cc-71-linux-package-distribution.md).

Authorized: local core/site branches and commits; unsigned candidate tooling, test-key-only local origin and serial Podman matrix; read-only p15 SSH/HTTPS. No deploy keys, real signing key use/copy, m720q, external publication, system installation or VMs without further approval.

## Acceptance

- [x] Commit plan/index first on feature branch.
- [x] Read-only host evidence, known-host verification and isolation recommendation recorded.
- [x] Unsigned package/release configuration, pinned toolchain/actions/base images, SBOM/checksums.
- [x] Native apt/RPM/pacman fixture indexes; throwaway signing key; local HTTP origin.
- [x] Install/upgrade/remove/version/quickstart and negative signature/hash tests, serial matrix.
- [x] Site install draft and identity-configurable deploy script, no deployment.
- [x] Record remaining gates and local commits.

Evidence accumulates in [phase-0 note](../../notes/cc-71-phase0-and-pilot.md).

Validation: core lint/tests; 11 serial container rows; 3 native-client signature/hash negatives; 12 promotion safety and 5 deploy-script tests; local OCI amd64/arm64 smoke/SBOM/checksums. Details and limits in the linked evidence note. Omarchy VM subsequently approved/completed; production credentials/backup/staging and public publication remain separate approval gates.
