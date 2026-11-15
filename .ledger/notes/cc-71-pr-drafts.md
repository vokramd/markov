# Prepared draft PR descriptions

## vokramd/markov

Title: Prepare unsigned Linux package and OCI release candidates

Build five shared engine-only native artifacts, pinned multi-platform OCI candidates, SBOMs and checksums. Replace tag auto-publication with unsigned candidate artifacts and native Arm validation, including PR runs. Packaging installs the CLI and quickstart without services or runtime state.

Validation: local eleven-row install/upgrade/remove matrix, Omarchy VM updater/reboot/Snapper recovery, native signature-negative tests, and five OCI layout tests. Native GitHub Arm CI is prepared but not yet executed. No production release or publication is included; local fixture provenance cannot pass production signing gates.

## vokramd/markovd.com

Title: Prepare native repositories, p15 signing and reviewed manual promotion

Add the install-page draft, native apt/RPM/pacman generation, host-only signing and strict bundle validation. Restricted deploy identities upload only to non-web inboxes; p15 manually promotes approved regular files in payload/history, indexes and InRelease order. Existing direct restricted publication is disabled. Deploy script accepts an explicit identity.

Validation: 62 repository-tool tests, five deploy tests, disposable-key full signing, three clean native-client signature checks, restricted inbox acceptance and local interruption/rollback tests. Live publication and real-key signing remain excluded. Shared DreamHost account isolation limitations are documented.
