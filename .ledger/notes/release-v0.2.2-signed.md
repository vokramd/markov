# v0.2.2 signed review and recovered replacement identity

Human explicitly approved replacing the unpublished Alpine key with a password
generated directly into KeePassXC. The previous encrypted identity is preserved
unchanged in `~/.local/share/markov-signing/alpine-retired-unpublished-20261119T035639Z`;
its earlier off-machine backup is untouched. The GPG identity is unchanged.

Replacement native public PEM SHA256:
`0d33b6a9c939d34db37f1b48f66158c416aabf824f3923fc615bababe7d731ef`.
The native password and independent backup password were generated and saved
directly into the encrypted vault, never displayed or persisted in plaintext.

Both encrypted native backup and KDBX were copied to the mounted 16-TB drive at
`jtanner@192.168.1.3:/mnt/wdc_16tb/BACKUPS/markov-signing/vault-alpine-p15-20261119T035739Z-517f4a`.
Retrieved hashes matched. The human entered the vault master password once in a
local GUI; the retrieved KDBX unlocked and contained the GPG/native/backup
credentials. The retrieved key archive decrypted, restored files matched, and
the restored RSA key signed a challenge verified with its public key. Native
receipt is now genuinely verified. Preserve these backups through CC-48/CC-70.
The older GPG password file is still retained pending explicit removal approval.

The first signing workspace failed closed when the vault idle-locked; see the
[fixed bug](../bugs/fixed/cc-71-vault-idle-during-signing.md). Both signers now
retrieve once at process start and clear mutable buffers on close. 107 safety
tests passed. Fresh signing in
`~/.cache/cc71/v0.2.2/stable/vault-signing-work` completed and verified.

- Real signed manifest SHA256: `cf745708efaeaf685c84559382ac3910117bdf8a1cb59a7542763a624016cf58`.
- 207 repository files; seven native package artifacts; actual tagged source and
  native-Arm CI unchanged from the prepared checkpoint.
- Fresh public-only keyring verified the explicit archive signing subkey,
  release manifest, final checksums and every final artifact hash.
- Real-key Debian 13, Fedora 45 and Arch containers passed installation,
  v0.2.1-to-v0.2.2 upgrade, version, quickstart, removal and preserved user data.
- Both Alpine architectures passed real-key install/version/quickstart/removal;
  prior-version upgrade is covered by the earlier disposable two-version tests,
  since this is the first public native Alpine release. Arm distro execution is
  emulated; independent native Arm CI is already green.
- All five representative native clients rejected corrupted signatures and
  package hashes: apt, rpm, pacman, APK x86_64 and APK aarch64.
- Final install page uses the actual new public-key hash and OCI digest. Its
  six-width Playwright review passed layout/anchors/no-JS and exact keyboard
  copying for all 20 command blocks. Nothing deployed.

Detailed [JSON receipt](release-v0.2.2-signed.json); local logs under
`~/.cache/cc71/v0.2.2/stable/`. Site commits `77938ae` and `368a160` hold vault
integration and the process-cache fix. These are local commits, not pushes.

Publication is still pending: no native/public key or site upload, GitHub
Release, or GHCR push has occurred. v0.2.1 remains live. The next step is the
previously authorized ordered publication and public acceptance, retaining all
historical versions. No further signing prompt is needed for these artifacts.
