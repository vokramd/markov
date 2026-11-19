# Dedicated KeePassXC signing vault

Human approved installing KeePassXC and chose/confirmed the master password in
local GUI dialogs. KeePassXC 2.8.0-2.fc44 is installed on p15. An empty encrypted
`~/.local/share/markov-signing/signing.kdbx` was created, CLI unlock verified,
then independently unlocked in the KeePassXC GUI by the human.

The dedicated instance runs on a private D-Bus under
`/run/user/1000/markov-signing-vault/`, with service activation disabled. The
existing desktop Secret Service provider PID 21433 was preserved. Browser and
SSH-agent integrations are disabled; idle lock configured at 900 seconds.
Screen-lock propagation on the private bus is not verified; explicitly lock
after use. Same-user compromise remains outside this isolation boundary.

Site worktree `markovd.com-v0.2.2-release` now has an opt-in
`release-bundle.py sign --use-signing-vault`, `signing_vault.py`, a dedicated GUI
launcher, and `repo-tools/SIGNING-VAULT.md`. Transport uses anonymous descriptors,
not secret arguments/environment/stdout. Source review, native identity and
backup gates remain. Fixture mode cannot use the real vault. Missing, locked,
ambiguous, wrong-owner/provider entries fail closed without fallback.

Validation: 104 tests passed (disposable GPG/native identities only), plus a
dedicated live dummy-vault adapter test for store/retrieve, missing entry and
locked-vault rejection. Cache evidence:
`~/.cache/cc71/v0.2.2/vault-test/` (run12 adapter acceptance; run10 original proof).
The initial sandbox suite could not start fixture GPG agents; rerunning with
approved home-disk temp/cache access passed.

The existing known GPG password was imported without displaying it; exact
retrieval verified in memory. Its original owner-only password file is preserved.
No signing keys were changed, no real signing occurred, no publication occurred.
Vault backup and restore are not yet complete; do not delete the older source.
Backup helpers have not yet been connected to the vault.

The unpublished Alpine key still has an unverified password. Asked the human
to choose between retrying it and replacing the unpublished key with a password
generated directly into KeePassXC while preserving the old key and backup.
Neither choice has been executed. No verified native backup receipt exists.
The release remains unsigned/unpublished; v0.2.1 remains live.

Latest: the human explicitly approved the replacement. It and the encrypted vault were backed up, retrieved and restore-verified, then the fresh release candidate was signed and tested. See [signed review](release-v0.2.2-signed.md). The previous pending-choice/recovery paragraphs above describe the earlier checkpoint.
