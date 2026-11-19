# Pongo2 patch record omits arithmetic error handling

Found during CC-71 admission research, 2026-11-19. `third_party/pongo2/MARKOV_PATCHES.md`
lists only the chained-subscript parser change. Comparing the vendored tree
with published upstream v6.0.0 identifies exactly two changed common files:
`variable.go` and `parser_expression.go`. The latter adds integer division and
modulo zero checks, introduced by commit `06e91fe14c4fe346b6c3ce4a3935ea5384ef5452`.

The existing record names v6.0.0 and retains the MIT license, but is insufficient
to explain the full fork delta during distro source/license review. Markov has
regressions in `pkg/template/native_test.go` and `pkg/template/template_test.go`.
Published upstream v6.1.0 was also inspected: its relevant arithmetic branches
and subscript parsing do not contain these local changes. No upgrade/removal
was tested, so replacing the fork cannot be presumed behavior-preserving.

Proposed fix, not implemented by research: document both patches, provenance,
omitted upstream tests/fixtures, and the regression coverage; decide whether
to pursue upstream inclusion or an explicit distro-approved downstream patch
strategy. Do not remove the replacement or weaken engine semantics simply to
make packaging easier. Upstream contact requires separate approval.

Research evidence: `~/.cache/cc71/admission-research/pongo2-comparison.json` and
comparison diffs; [admission report](../../plans/014-cc-71-distro-admission-research.md).
