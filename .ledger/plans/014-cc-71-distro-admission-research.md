# CC-71: distro admission requirements and Markov readiness

Research date: **2026-11-19**, p15. Status: research completed; no recipes,
packages, accounts, external issues/PRs/ITPs, submissions or maintainer contacts
created. Applies only to the Markov engine. First-party v0.2.2 remains unchanged.

## Recommendation and decisions

Pursue **Omarchy first, Fedora second, Debian/Ubuntu after dependency discovery**.
There is a documented route for each, but none is ready for submission today.
The existing binary repositories establish delivery evidence, not acceptance
into a distro-maintained archive.

| Target | Verified route | Recommended next work | Submission readiness |
|---|---|---|---|
| Omarchy's own repository | Package recipe PR to `omacom/omarchy-pkgs` | Prepare a private/local x86_64 binary-recipe prototype after shared documentation cleanup | Conditional; package selection/build approval belongs to maintainers |
| Fedora | Source RPM review and packager permissions | Local source/vendor/license proof using Fedora Go tooling | Not yet: distro spec, vendor archive and license review absent |
| Ubuntu through Debian | Debian source package, upload/review, then applicable Ubuntu import | Inventory Debian build-dependency versions and fork treatment first | Not yet: concrete dependency gaps and source packaging absent |

Recommended human choices before implementation:

1. Name a continuing maintainer and backup reviewer; confirm any existing Fedora
   packager or Debian upload/sponsorship relationships. Do not assume the human
   is a new contributor or already has those rights.
2. Start Omarchy with **x86_64 only**, matching approved pacman scope. Existing
   Linux arm64 tarballs do not constitute arm64 pacman/Omarchy validation.
3. Prefer an initial `markov-bin` proposal based on tested release tarballs for
   Omarchy; leave source-built `markov` as the alternative if maintainers prefer
   it. Confirm naming and migration from the existing first-party `markov`
   package before implementing. No rename is approved or performed here.
4. Use Fedora's vendor workflow; do not plan blanket unbundling there. Treat
   Debian's dependency/fork plan separately.
5. Authorize local preparation separately from any external submission/contact.
   No plan estimate promises acceptance, a sponsor, or a release date.

## Local evidence and shared readiness gaps

Inspected merged engine source at `718f611c2cba1b112d62382de6f2a63e1b7e94ba`,
the released v0.2.2 source archive/tag, and the existing packaging definitions.
The documentation branch changes no engine behavior.

| Item | Evidence | Required next action |
|---|---|---|
| Releases and source | v0.2.2 has signed checksums/manifest, source tarball, SBOMs and tested binaries. Source archive contains root `LICENSE`, `go.mod`, `go.sum`, local Pongo2 source and its MIT license. | Verify a distro source build from that source, not from the prebuilt deb/rpm. Never rewrite published v0.2.2 bytes. |
| Dependency size | Offline `go list -m -json all` reports **170 graph entries including the main module**. The prior binary license inventory covers 86 linked modules across release targets. These are different populations. | Map build/test dependencies, not just the runtime notices. No exhaustive archive-availability mapping was performed here. |
| Compiler | `go.mod` requires Go **1.25.13**. The OpenShell SDK requires the same; SQLite/Kubernetes dependencies also need modern Go. | Use declared distro toolchains without automatic downloads in build workers. Debian sid currently lists a 1.27 default, so the compiler alone is not the current sid blocker. |
| Pongo2 fork | `replace` points to `third_party/pongo2`; upstream v6.0.0 comparison finds exactly two changed common files and one added patch record. | Complete fork provenance, preserve both fixes and decide distro handling. See below. |
| Licenses | Root Apache-2.0, Pongo2 MIT, generated module notices/inventory already exist. Collector inventories root license/notice files; it is not a complete distro copyright decision. | Review actual source/vendor contents with target licensing tools; preserve notices and document scanner exceptions. |
| Manuals | No tracked `*.1` manpage or manpage generator was found. Cobra CLI help exists. | Add maintained/generated CLI documentation and installation paths in a separately assigned readiness change. |
| Maintenance | Human-selected best-effort first-party maintenance exists; no tracked `SECURITY*` document was found. | Publish a security contact/update policy and agree reviewer/CVE/dependency responsibilities before volunteering distro maintenance. These are proposed readiness improvements, not invented universal admission rules. |
| Tests/build flags | Go tests and release CI exist; first-party builds use static GoReleaser flags and `-X main.version`. | Prove offline source builds and classify tests needing network/system services. Preserve distro hardening/flags and encode the version; do not reuse stripped upstream flags blindly. No build or package experiment was run in this research. |

### Pongo2 is a bounded, behavior-relevant fork

Comparing the local tree with the [public module archive](https://proxy.golang.org/github.com/flosch/pongo2/v6/@v/v6.0.0.zip) for **v6.0.0** found
changes only in `variable.go` and `parser_expression.go`; `MARKOV_PATCHES.md` is
the only local-only file. There are 129 omitted upstream files, including the
tests/docs/fixtures intentionally excluded when the copy was introduced.

The documented fix continues parsing after a subscript. The undocumented fix
turns integer division/modulo by zero into template errors. Source commits
`a2f189e` and `06e91fe` establish provenance; Markov regressions are in
`pkg/template/native_test.go` and `pkg/template/template_test.go`.
The [open provenance bug](../bugs/open/pongo2-patch-provenance-incomplete.md)
records the missing documentation; it was not fixed as part of research.

Published upstream [v6.1.0](https://github.com/flosch/pongo2/tree/0d8aa98add72a5076299f6af875f720c6957c7fb), commit
`0d8aa98add72a5076299f6af875f720c6957c7fb`, was also inspected. The relevant
subscript/arithmetic branches still lack those local changes. This is source
inspection, not a tested upgrade assessment. Do not assume an upgrade can
remove the replacement. Possible future approaches are upstream inclusion,
an explicit distro patch series, or approved vendoring with complete provenance.
No upstream contact or dependency change occurred.

## Omarchy: its repository, not AUR or Arch admission

The [current repository instructions][O1] accept a package-scoped PR against
`master`: `pkgbuilds/<name>/PKGBUILD` plus `.omarchy/package.json`. Sources must
be pinned/checksummed, supported architectures declared, and upstream updates
described. Unknown contributors require maintainer build approval. Required
checks include package results, repository self-tests and build isolation.
Accepted packages enter `edge`; `rc`/`stable` require the applicable promotion.
Repository inclusion is not default installation in the OS image.

Inspected repository tree `ffba05a4e2bdddbd494a8a92218ad87a39c22c2b` contained
346 recipes and no Markov recipe. That is a bounded observation, not a promise
that no pending request exists. Its documented architecture options do not
oblige us to expand our approved x86_64 target.

Two accepted comparisons at that exact tree:

- [`yay`][O2]: source-built Go CLI, declared Go build dependency and build flags;
  its [metadata][O3] watches stable GitHub releases. It demonstrates a source
  recipe is supported, not that its libalpm/CGO choices suit Markov.
- [`crush-bin`][O4]: GoReleaser-produced release tarballs, per-architecture
  checksums, docs/completions/manpage installation and explicit package
  conflicts/provides. Its [metadata][O5] demonstrates release watching. Its
  fast-ring setting is package-specific; do not request it by default for Markov.

Proposed local deliverable: a real release URL/checksum recipe with correct
license notices, package naming/conflict migration, stable-tag watch, and
install/update/removal tests against intended Omarchy channels. Existing
`packaging/arch/PKGBUILD.in` consumes a staged `markov.tar.gz` and lacks Omarchy
metadata; it is not a submission-ready recipe. If source signatures are used,
follow the repository's recipe-local public-key process rather than fetching
keys during builds. Distro maintainers sign their published packages; never
provide our private archive key.

[AUR][A1] distributes community recipes; [Arch official repositories][A2] have
their own maintainers and admission. Neither AUR publication nor our existing
`/repos/omarchy/rolling/` root constitutes Omarchy repository acceptance.

## Fedora: source RPM, vendor workflow and separate sponsorship

The current [new-contributor process][F1] and [review process][F2] require a
reviewable spec/SRPM and reviewer approval. Contributors without packager
membership also need sponsorship; review and membership are separate gates.
After approval/permissions, request dist-git ownership and branches, import
the reviewed sources, build through Fedora infrastructure and follow its update
process. Our first-party nFPM RPM is not that source package.

The canonical [Go guidelines][F3], read directly on p15, now require vendored
dependencies for **new** Go packages. They call for reproducible vendor inputs,
module `bundled()` metadata, cumulative SPDX licensing, individual dependency
license files, and `go-vendor-tools.toml`. Preserve Fedora Go build flags using
its macros; the old `%gocheck` approach is deprecated. Review the license report
before uploading sources; run compatible tests and explain exclusions. Thus
our top-level `Apache-2.0` package field and collected notices are useful input,
not a ready Fedora license/spec result.

The [Go Vendor Tools scenarios][F4] describe `go2rpm --profile vendor`, vendor
archive generation and licensing review; its [configuration reference][F5]
supports explicit corrections. Proposed local proof: source archive + vendor
archive + reviewed configuration, with the local Pongo2 source demonstrably
preserved, offline dependencies, version metadata and documented tests. This
was researched, not generated or built.

Accepted comparison: [Fedora `gh`][F6], inspected [rawhide spec][F7] for 2.104.0,
uses separate upstream/vendor/config sources, cumulative licensing, Go build
macros, generated manual/completion files and license/test checks. We read
packaging metadata only; no `gh` executable was installed/downloaded.

A [Forge-PR review change proposal][F8], updated 2026-11-13 and targeting Fedora
46, is explicitly a proposal; the checked current process still documents
Bugzilla review. Reverify adoption immediately before submission instead of
treating a proposal or old wiki draft as the operative process. Do not invent
a sponsorship requirement if the human already has current packager rights.

## Debian first, then Ubuntu archive import

The [Debian new-package process][D1] starts with duplicate-work checks and an
ITP; [mentors guidance][D2] describes source preparation, sponsorship and NEW
review. A maintainer without upload rights needs an uploader; source copyright
and archive review remain required even with a sponsor. Package maintenance
continues after initial acceptance. No ITP, RFS, mentors upload or team contact
has been made.

The [Go team guide][D3] uses distro Go packaging helpers and declared library
build dependencies. [Policy on embedded code][D4] prefers archive libraries
over convenience copies, with qualifications; do not assert all vendoring is
universally forbidden or reuse Fedora's vendor rule. Proposed deliverables
include `debian/control`, `rules`, `copyright`, source format/patches, watch,
tests and required documentation; an nFPM binary deb is not a Debian source
package. [Manual-page policy][D5] makes the missing manpage a concrete gap.

Accepted comparison: Debian [`gh`][D6] 2.46.0-4, its [control][D7] and [rules][D8],
uses Go-team maintenance, numerous versioned library Build-Depends, `dh-golang`,
generated manuals/completions and an explicit `debian/go` source overlay.
That last detail is a reason to investigate justified exceptions, not copy a
large bundled tree without explanation.

Two concrete dependency findings prevent a submission-ready estimate:

- Live sid [`golang-modernc-sqlite-dev`][D9] was **1.40.0+dfsg-2**; Markov
  requires **1.49.1**. Packaging/updating that dependency or proving a compatible
  alternative requires a separate assessment, not a blind version downgrade.
- The package-name search for OpenShell found unrelated IFCOpenShell packages,
  not the NVIDIA Go SDK. It is a compiled import in `pkg/openshell/sdk.go`, not
  merely an optional external CLI. Absence from this search does not prove no
  source package under another name exists. Map the import and its closure.

Sid's [`golang-go`][D10] currently resolves to Go 1.27, newer than our minimum.
This does not prove the full dependency set builds; no sbuild test was run.
The 170-entry module graph is an upper investigation surface, not a claim that
169 new Debian packages are needed. Do not silently remove OpenShell features
or replace SQLite to reduce packaging work.

[Ubuntu's new-package process][U1] recommends Debian-first admission; its
[sync rules][U2] describe importing from Debian unstable during the development
window and freeze-dependent exceptions thereafter. Target a development
release after Debian readiness. Acceptance does not automatically add a new
package to Ubuntu 24.04/26.04, put it in `main`, or promise Canonical support.
A PPA remains a separate third-party delivery mechanism and does not meet the
human's official-archive goal.

## Duplicate-work check and verification limits

The existing local admission task/plans are the known overlap. Authenticated
all-state Markov/site issue listings each returned eight PR entries and no
non-PR issues; no additional issue work was found. This did not query CC.

No Markov recipe appeared in the inspected Omarchy tree. Exact Arch official
and AUR queries returned zero matches. Debian/Ubuntu name searches returned
unrelated Markov-chain packages, not this project; Debian's being-packaged
list did not identify it. The Fedora package page for `markov` returned 404.
These bounded checks do not reserve a name or exhaust pending external review
queues, differently named packages or every WNPP request. Repeat target-specific
queue/name checks before any separately authorized submission.

Canonical Fedora pages were blocked through the browser research tool but
ordinary HTTP on p15 returned the real documents. Their captured text/checksums
support the findings above; no challenge-solving or access-control bypass was
used. Raw snapshots remain outside git. Source policies can change.

## Work order, effort and acceptance gates

Estimates are engineering effort, not elapsed sponsor/review time:

| Work | Proposed effort | Exit evidence |
|---|---|---|
| Shared readiness | 2–4 days initially | Complete fork record, manual/help plan, maintenance/security ownership, source/license gap inventory |
| Omarchy local recipe proof | 1–3 days after shared readiness | Approved source-vs-binary/name/arch choice, pinned recipe/watch, clean package checks |
| Fedora local source/vendor proof | 3–7 days initially | Reviewed license/vendor sources, compliant local build/tests, maintainer-rights assessment |
| Debian dependency discovery | 1–2 days first pass | Import-to-package/version map, SQLite/SDK/fork options and dependency work estimate |
| Debian/Ubuntu source prototype | 3–7 days only after dependencies are viable | Rebuildable source package, copyright/manuals, clean tests and sponsor-ready evidence |

Dependency remediation can add weeks; review/sponsor/NEW/promotion waiting has
no defensible fixed bound. No evidence supports promising a submission date.

**Go now:** approve a bounded shared-readiness change and an Omarchy local
prototype, then Fedora's local source/vendor proof. **Hold Debian/Ubuntu
submission:** finish dependency discovery and get a reviewed feasible strategy.
All external forks, PRs, ITPs, uploads and sponsor/maintainer contact require
explicit approval after local artifacts are concrete and reviewable.

Maintenance plan before any submission: a named owner handles upstream releases,
dependency/CVE rebuilds, distro bugs, branch/freeze rules and package retirement.
First-party and distro build revisions/trust roots stay distinct. Plan migration
instructions so users do not accidentally mix first-party and distro providers.
Existing p15 private keys are never an admission credential handed to a distro.

## Primary sources checked

[O1]: https://github.com/omacom/omarchy-pkgs/blob/ffba05a4e2bdddbd494a8a92218ad87a39c22c2b/README.md
[O2]: https://github.com/omacom/omarchy-pkgs/blob/ffba05a4e2bdddbd494a8a92218ad87a39c22c2b/pkgbuilds/yay/PKGBUILD
[O3]: https://github.com/omacom/omarchy-pkgs/blob/ffba05a4e2bdddbd494a8a92218ad87a39c22c2b/pkgbuilds/yay/.omarchy/package.json
[O4]: https://github.com/omacom/omarchy-pkgs/blob/ffba05a4e2bdddbd494a8a92218ad87a39c22c2b/pkgbuilds/crush-bin/PKGBUILD
[O5]: https://github.com/omacom/omarchy-pkgs/blob/ffba05a4e2bdddbd494a8a92218ad87a39c22c2b/pkgbuilds/crush-bin/.omarchy/package.json
[A1]: https://wiki.archlinux.org/title/Arch_User_Repository
[A2]: https://wiki.archlinux.org/title/Official_repositories
[F1]: https://docs.fedoraproject.org/en-US/package-maintainers/New_Package_Process_for_New_Contributors/
[F2]: https://docs.fedoraproject.org/en-US/package-maintainers/Package_Review_Process/
[F3]: https://docs.fedoraproject.org/en-US/packaging-guidelines/Golang/
[F4]: https://fedora.gitlab.io/sigs/go/go-vendor-tools/scenarios/
[F5]: https://fedora.gitlab.io/sigs/go/go-vendor-tools/config/
[F6]: https://packages.fedoraproject.org/pkgs/gh/gh/fedora-45-updates-testing.html
[F7]: https://src.fedoraproject.org/rpms/gh/raw/rawhide/f/gh.spec
[F8]: https://fedoraproject.org/wiki/Changes/PackageReviewProcessForgePR
[D1]: https://www.debian.org/doc/manuals/developers-reference/pkgs.html#new-packages
[D2]: https://mentors.debian.net/intro-maintainers/
[D3]: https://go-team.pages.debian.net/packaging.html
[D4]: https://www.debian.org/doc/debian-policy/ch-source.html#embedded-code-copies
[D5]: https://www.debian.org/doc/debian-policy/ch-docs.html#manual-pages
[D6]: https://packages.debian.org/sid/gh
[D7]: https://sources.debian.org/src/gh/2.46.0-4/debian/control/
[D8]: https://sources.debian.org/src/gh/2.46.0-4/debian/rules/
[D9]: https://packages.debian.org/sid/golang-modernc-sqlite-dev
[D10]: https://packages.debian.org/sid/golang-go
[U1]: https://ubuntu.com/project/docs/how-ubuntu-is-made/processes/new-packages/
[U2]: https://ubuntu.com/project/docs/how-ubuntu-is-made/processes/merges-and-syncs/

Capture metadata and local findings: [research receipt](../notes/cc-71-distro-admission-research.json).
Raw documents/comparison data: `~/.cache/cc71/admission-research/` on p15.
