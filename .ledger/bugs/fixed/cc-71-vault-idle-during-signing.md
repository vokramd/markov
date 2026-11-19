# Vault idle locking interrupted a release process

The first vault-backed real signing attempt read GPG credentials once per
signature. KeePassXC's idle lock activated during RPM metadata signing, so the
release failed closed before publication. The partial workspace is retained at
`~/.cache/cc71/v0.2.2/stable/unsigned-signing-work` with failed state. Never resume
it or publish its partial output.

Fix: both GPG and native signers retrieve credentials when their release process
starts, keep mutable buffers only for that process, and clear them on close.
New processes still require an unlocked vault. Locking the vault does not revoke
already retrieved in-process credentials; terminate that process to stop it.
Temporary library/Python memory copies remain a limitation.

Regression test signs twice with a protected disposable GPG key, asserts a
single vault retrieval, checks buffer zeroing and rejects reuse after close.
All 107 safety tests passed. Site commit `368a160` contains the fix; operator
work starts fresh in `stable/vault-signing-work` after checking actual unlocked
state. No idle timeout was extended and no desktop keyring setting changed.
