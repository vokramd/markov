# CC-71 v0.2.0 launch review

Prepared locally after the human requested the final install page and publication checklist. Site branch cc-71-launch-review, commit 29eab6307dbe162462d66de6260d559d5397ac95. Launch-ready source describes the proposed published state; it is not deployed.

Review sibling site files install/index.html, repo-tools/PUBLICATION-CHECKLIST-V0.2.0.md and repo-tools/RELEASE-NOTES-V0.2.0.md. Only three static files would deploy: install/index.html, index.html (one navigation link), sitemap.xml. Exact local snapshot manifest a32d452ae204d737a9819039485424615085ff7cab1c4134b90c859b60732a13. No .htaccess/routing/DNS/server code or unrelated site changes. Local plan at ~/.cache/cc71/v0.2.0/launch-site-plan, published=false.

Validation: 12 actual HTML Bash snippets parsed, real certificate accepted and wrong/additional/malformed certificates rejected. Five deploy tests passed. Exact OCI archive ran documented rootless caller-UID quickstart with host-owned persistent report/state; a local Skopeo all-platform copy preserved the signed index digest. XML sitemap and homepage link valid. Public homepage/sitemap match merged baseline; install/ currently 404. Evidence in ../../notes/cc-71-publication-review.json. Broader package/signing/native Arm/Omarchy evidence remains separate.

The designated GitHub API token declares write:packages; no registry authentication/write was attempted. GitHub currently requires classic PAT or repository workflow token for GHCR; package defaults private, so actual namespace authorization and explicit public visibility/anonymous pulls are publication prerequisites. Never persist the token or pass it in arguments. Use an on-demand private credential protocol/client and only the designated token file at call time.

Pending human approval: push/open/merge the two launch-review PRs, then perform the explicitly listed outward Release/certificate/native repository/GHCR/site actions. Draft Release uploads also require approval. No publication, remote writes, new tags, re-signing, VM creation or official-distro submission in this preparation. Keep the exact signed v0.2.0 artifacts/tag, refreshed verified backup and all historical versions.

## Approved execution checkpoint

Launch approved; both PRs merged, Release staged with 26 verified assets, exact GHCR image uploaded privately. Public visibility and DreamHost account-quota confirmation need human action; native repositories/site not yet written. See [execution evidence](../../notes/cc-71-publication-progress.md). Earlier pending-approval text is historical.

Public GHCR now verified anonymously on both architectures (arm64 emulated). Only DreamHost account quota remains pending; Release stays draft, native repositories/site unchanged. See execution evidence.
