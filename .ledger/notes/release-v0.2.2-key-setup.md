# v0.2.2 native key setup — backup pending

Human said ready at p15. Approved initializer created a dedicated encrypted RSA4096 PKCS8 identity in the private fixed native home; two GUI passphrases matched and public derivation verified decryption. No password was written to a file, logs or Git. Exact public/ciphertext hashes are in the companion receipt; no key material is committed.

m720q read-only checks confirmed /mnt/wdc_16tb is the actual mounted ext4 16-TB disk, BACKUPS owned by jtanner, with 7.9 TB available. Existing archive-key backup ciphertext matches its canonical SHA256 at both the old 8-TB path and the migrated 16-TB path.

Native backup helper cryptographic roundtrip self-test passed. Its first backup GUI prompt was cancelled or unavailable, before backup encryption or remote writes. The new native identity remains created-not-backed-up; no verified native backup receipt exists. A readiness question is pending to reopen just the backup dialog. Do not rerun native-key initialization, overwrite the identity, infer a verified receipt, sign release artifacts or publish the key before retrieved-backup restore/sign/verify succeeds.

Current backup helper is ~/.cache/cc71/v0.2.2/native-backup-tools/backup.py. It chooses/verifies a different backup password, proves it cannot unlock the native key, independently encrypts, uploads only ciphertext to a new directory on the mounted 16-TB disk, retrieves/hashes/decrypts, then prompts for the native key password to test restored-key signing. Credentials stay in memory/descriptors. Source/CI approval is prepared privately; signing is still gated on verified backup. No packages, keys, site, GitHub Release or GHCR image were published.
