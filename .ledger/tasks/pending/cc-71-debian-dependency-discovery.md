# CC-71 proposed extended Debian dependency closure audit

Pending assignment beyond the completed initial [admission research](../../plans/014-cc-71-distro-admission-research.md).

Map actual imported/build/test Go modules to current Debian source/binary
packages and versions. Start with modernc SQLite (observed sid 1.40.0 versus
required 1.49.1), NVIDIA OpenShell SDK, Kubernetes dependencies and patched
Pongo2. Check alternative package names and policy-approved handling before
calling a dependency absent. Distinguish the 170-entry module graph from the
86-module release-binary license inventory; neither implies 169 new packages.

Deliver a reproducible coverage table, missing/update/fork work estimate and
go/no-go for a Debian source prototype, then an Ubuntu development-release route.
Do not silently downgrade libraries, remove OpenShell functionality, promise
existing-LTS admission, or substitute a PPA for archive acceptance.

No ITP/RFS/upload, team/sponsor contact, source/dependency changes or package
builds under this task until separately assigned. Initial inventory estimate
1–2 engineering days; dependency remediation may add weeks.
