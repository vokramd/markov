# CC-71 phase-2 draft PR text (not submitted)

## Engine

Title: Add unsigned Alpine APK candidates and record phase-two validation

Add APK output to the existing unsigned GoReleaser pipeline with an APK-only r0 revision. Record the first-party RHEL/UBI, Rocky, Alma, openSUSE and Alpine support matrix and local validation. Scope remains the engine CLI; no markovd packaging or official-distro endorsement.

Validation: fresh local Go 1.25.13 / GoReleaser 2.20.2 snapshot produced seven native artifacts, archives and SBOMs. Corresponding native binaries passed clean-client quickstart/upgrade/removal tests. Hosted/native-Arm CI and a clean production tag/candidate remain pending. Fixture version 0.2.2 is not a proposed release.

## Site/repository tooling

Title: Extend native repositories and integrate encrypted host-only Alpine signing

Add explicit first-party RPM roots for RHEL/UBI, Rocky and Alma 9/10, plus Leap 16.1 and Tumbleweed rolling; reuse the same signed RPM bytes. Add Alpine v2/RSA256 host signing with encrypted native-key identity/backup approval, descriptor-only secret handoff, public-only container indexing, complete candidate/request binding and signed final artifacts. Preserve the five-artifact release path; require both APK architectures when present. Keep the fixture-only proof guard.

Validation: earlier distro pilots covered all 18 added container rows. Final encrypted-signing acceptance verified two seven-artifact fixture bundles, five clean-client install/upgrade/removal rows (both Alpine architectures and apt/dnf/pacman), five native signature/integrity rejection rows and 93 safety tests. Independent checks verified manifests/checksums and APK/archive/SBOM binary lineage. Arm64 was emulated locally. No live install page, production native key, backup/signing, hosted CI or publication changed. Operator review/receipt templates and exact evidence are in repo-tools/HOST-NATIVE-SIGNING.md and HOST-NATIVE-SIGNING-RESULTS.json.
