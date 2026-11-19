# Approved post-release merges and plaintext credential cleanup

The human approved merging both release review PRs and removing the older
plaintext GPG password source after verifying vault recovery evidence.

- Engine PR #8 merged as `718f611c2cba1b112d62382de6f2a63e1b7e94ba`.
  Candidate, test and native-arm64 checks passed on its exact reviewed head.
- Site PR #8 merged as `171420ba914605e98205987e22f7e5f77c8daa33`.
  The site has no hosted CI; its recorded local/browser checks already passed.

Before cleanup, the live encrypted KDBX and retrieved backup matched the
restore-verified vault hash. Read-only SSH confirmed the matching encrypted
vault and native-key archive still exist on m720q's mounted 16-TB backup drive.
The receipt proves recovery included the GPG, native and backup credentials.
The old password file had not changed since that verified backup.

Removed only `~/.local/share/markov-signing/passphrase`, after checking its
owner, mode 600, regular-file type, single link and exact inode. Its absence
was confirmed. This was unlinking, not a secure-erasure claim. No password
was printed or copied into this repository, and no new unlock prompt was
needed because the current vault still exactly matched the restored backup.

Encrypted key/vault backups, the archive GPG identity and retired unpublished
Alpine identity remain preserved. Published packages/site/image tags were not
changed. The earlier release closeout's statement that the plaintext source
was retained describes its historical state before this explicit approval.

Private operator receipt: `~/.cache/cc71/v0.2.2/plaintext-source-removal.json`.
