# Native backup retrieved/restored; key password verification pending

2026-11-18 local / 2026-11-19 UTC. The native backup was independently password-encrypted, uploaded to the actual m720q 16-TB drive, retrieved with matching SHA256 and decrypted successfully. Restored encrypted-private/public bytes match the trusted originals. Both subsequent native-key unlock entries failed. The second attempt used a tested PySide6 Show-passphrase dialog.

No verified native-backup receipt exists, and no real release signing/publication is allowed yet. Exact partial evidence is in the companion JSON, deliberately status!=verified and test_restore_sign_verify=false. Encrypted restored files are retained mode 600 under the private native-backup workspace for a key-only retry; no password value is retained. The original native key and all encrypted backups remain intact.

Human choice requested: retry the native key password, explicitly approve a fresh unpublished native key with visible choose/confirm and uncached unlock verification, or pause. Do not silently rekey. The existing OpenPGP archive identity is unaffected. If a fresh key is approved, preserve/quarantine the old native identity and keep its backups; update unpublished fingerprints/approval drafts only after new-key recovery verification. No public key, package, Release, GHCR or site bytes changed.
