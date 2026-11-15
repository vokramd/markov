# CC-71 non-web inbox and p15 promotion acceptance

2026-11-15 UTC, p15/m720q/DreamHost. **Approved replacement acceptance passed; stopped for human review.** [Structured evidence](cc-71-nonweb-inbox-acceptance.json), [deploy backup receipt](cc-71-deploy-backup-receipt.json). No live site/package/archive-key/GitHub/GHCR publication.

## Cleanup and live baseline

Removed the whole exact inventoried `~/markovd.com/cc71-staging/` tree. All three previous canary URLs return **404**; final recheck also confirms inbox visibility/code/static-fixture probes return 404/403 (12 URLs). Baselined live index/styles/app/robots/sitemap/root .htaccess hashes matched before cleanup and after all tests. No public `/repos` root was created.

The cleanup-stage authorized_keys hash matched its original baseline. Provisioning then appended exactly the TWO approved restricted inbox entries: final full hash differs as intended, but every original byte/personal key is preserved. Mode remains 600. No other credential entries changed.

## Keys, inboxes and backup

| Identity | Live private custody | Fingerprint | Restriction |
|---|---|---|---|
| Site | p15 `~/.local/share/markov-deploy/site_ed25519` | `SHA256:EcUTEH9SVBM3SS91esnTQk/t1JHO9rD3rDid83763kY` | `restrict`, rrsync `-wo -no-del -munge /home/tannerjc/markov-uploads/site` |
| Packages | m720q `~/.local/share/markov-deploy/packages_ed25519` | `SHA256:2WXUDAJKv6BW4lOJmU36kmWoan6l/rHor1UwpMlJcMc` | `restrict`, rrsync `-wo -no-del -munge /home/tannerjc/markov-uploads/packages` |

Inboxes are private 700 directories outside the identified site/service roots; identities are 600. Site identity is AES256-CTR protected with bcrypt 100 rounds; only this temporary test identity was removed from the desktop agent after acceptance. Package identity is intentionally passphrase-less for unattended inbox-only upload, as approved. It was never persisted as a live private file on p15.

Checked resolved project roots, legacy service directories and root symlinks; markovd.com/tannerjc.net inbox probes are 404. Legacy git/svn names do not resolve, and are not counted as successful HTTP tests. Accessible apachectl dumps container defaults, not a complete provider virtualhost inventory. Future alias/root changes require rechecking the boundary. Existing personal shell keys/shared Unix UID remain broader trust; a separate Markov DreamHost shell user is a **non-blocking follow-up**, not provisioned here.

Both identities/public companions/recovery notes are independently encrypted at m720q `/mnt/seagate_8tb/BACKUPS/markov-deploy/p15-m720q-2026-11-15T035803Z/`. Ciphertext SHA256 `46079021b9366dda788295319c2402cf006a371bde02bf8541d8038e4d21b7d4`; retrieved checksum, decryption, exact private/public bytes and restored package-key public derivation passed. Package restore used a RAM-only fd. Site restored bytes equal the protected source key that subsequently authenticated successfully. Backup directory/files 700/600; human-controlled backup secret not persisted. The site key's own passphrase must remain in the human's password manager. CC-70 inventory/CC-48 migration references are in notes/receipt; CC itself and drive migration untouched. The previously verified archive-key backup was neither replaced nor re-exported.

## Acceptance results

- **30 real restricted-identity SSH/rsync checks:** 15 each from m720q/package key and p15/protected site key. Intended upload works. Shell, arbitrary command, PTY, download, delete, parent/sibling escape, public-root dry-run, custom rsync-path command and remove-source options refused. Pre-existing symlink attack with `-K` refused. Uploaded symlinks are munged; hardlinks/config fixtures stay inert in inboxes. Outside-inbox guards, original live files, p15 quarantine/promoter and original authorization bytes unchanged.
- **Actual p15 pulls:** signed package fixture snapshots verified for old/new/rollback; independently reviewed static-site manifest verified and private plan bytes match. Real uploaded symlink/hardlink/dotfile/config/server-code/unlisted inputs rejected. A deterministic metadata race injected into the trusted reader against an inbox fixture was rejected. No input code executed or archive unpacked.
- **47 local safety tests passed** (`/tmp/cc71-inbox-tests.log`): signature/hash/path/type/link/race/resource checks, reviewed site digest, full retained history, failure stops, pinned SSH/identity constraints, production fixture rejection for package and site, and old direct-webroot execution disabled.
- **Real local rsync/loopback HTTP promotion:** history → payloads → indexes → InRelease; interruption before indexes preserved old InRelease, retry succeeded, roll-forward rollback restored known old dummy payload under a new fixture version with fresh signed metadata. All three package versions and three complete signed histories retained and independently verified.

Promotion fixtures are small inert transport data, not new real package releases or native install claims. Prior eleven distro containers/Omarchy VM remain native install evidence. Live admin-SSH public promotion is deliberately unexecuted pending approval; no live HTTP repository/real archive-key publication claim.

## Approved operating model

All trusted work runs on **p15**: optional admin-SSH inbox pull, bounded link/race-safe private snapshot, pinned manifest/hash/exact-file validation and native/static path allowlists. `inbox.py` executes only trusted read-only reader code inline; nothing installed on DreamHost beyond inbox data and approved key entries. `manual-promote.py` defaults to network-free planning; its PUBLIC `--execute` path still requires explicit human approval, clean production evidence and p15's existing desktop-agent admin access. No server-side promoter or new unattended shell/admin identity.

For phase 1, **skip the package inbox for manual releases**: sign/validate the local signed bundle directly on p15 and promote reviewed bytes there. This avoids an unnecessary inbox round trip without weakening the validation boundary. Keep the provisioned inboxes for future unattended uploads; they do not grant unattended publication. Old direct restricted-key `publish.py --execute` remains disabled. Public promotion preserves packages/history first, indexes next, InRelease last and all-version retention. Readiness/CI/signing and all public release/site/GitHub actions remain separate work/gates; stop here as the human requested.

Private helper/log/fixture artifacts are under `.cc71-vms/inbox-acceptance/` outside Git, plus small disposable `/tmp` fixtures. Remote test fixtures remain only inside private non-web inboxes for review. During setup, missing/closed GUI prompts and a missing Python bcrypt backend were handled before authorization: final protected site key used tested installed OpenSSH/private PTY, with no system package installation. An unprotected uninstalled intermediate was removed; no unprotected site key was authorized. No passwords/private keys were printed or tracked.
