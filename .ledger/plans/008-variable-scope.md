# Variable scope: globals, parameters and locals

Created 2026-10-25. Comes out of the plan 006 trial: iteration 6's candidate run was killed by
the kernel at 13.6 GB. Plan 009 adds the limits that stop any one run doing that; this plan
removes the cause, which any workflow that fans out over a fan-out could hit.

**Status:** phases 1-4 done (2026-10-25). See "As built" for where it differs from the plan.

## What happened

The candidate fetched each open issue's comments, then each open PR's comments, then each PR's
reviews, in three typed `for_each` steps with `register`. A `for_each` register collects each
iteration's *whole context*, and that context starts as a copy of the parent's, so:

- `issue_comments`: 120 elements, each holding all of `issues` (0.3 MB): 37 MB;
- `pull_comments`: 3 elements, each holding all of `issue_comments`: 112 MB;
- `pull_reviews`: 3 elements, each holding both: 448 MB.

In memory these are shared references, so cheap. Serialised they are not: a `set_fact` that
bundled the four registers (`"{{ issue_comments }}"` and so on) rendered to 562 MB on the first
agent run and 1.29 GB on the second. Each was written to the event log (1.85 GB), to the state
db (565 MB), and deep-copied by the plan 007 event masker. About 1 MB of real data.

Sub-workflows have the same problem: a child starts with a copy of the caller's whole context,
and `register` stores the child's whole final context, the copy included.

## Approach

Reading the parent is right for a step in the same workflow. Inheriting it is wrong for a
workflow call: a called workflow is a function, and sees what it is given plus the globals.
There are three kinds of variable.

| Declared | Kind | Seen by |
|---|---|---|
| File-level `vars:` (`vars.yaml` in a directory workflow) | **global** | every workflow in the run |
| A workflow's own `vars:` | that workflow's **parameters**, with defaults | that workflow; callers fill them |
| `register`, `set_fact`, a loop's `as` | **locals** | the workflow that made them; a child only if passed |
| `markov_*` | built-in | everywhere |

### Globals

The file-level vars are the globals: where a var is declared is what makes it global, with no
separate list. The github-workflow-example `vars.yaml` already opens with "Variables available
to every workflow."

- Their values are set when the run starts (file `vars:`, then `--var` and vars files) and do
  not change during it. A step that registers or `set_fact`s a global's name is a parse error,
  so concurrent iterations never share mutable state.
- A call may **shadow** a global for the child and everything below it, by passing the name in
  its `vars:` or `pass:`. github-workflow-example needs this: `vars.yaml` declares
  `GITHUB_TOKEN` (empty) and every call in `github-agent-cycle` passes
  `{{ bootstrap.github_token }}`. Shadowing changes nothing anyone else is reading.
- A value only some workflows should see does not belong in the file-level vars. It goes in the
  `vars:` of the workflow that uses it, and its caller passes it. `GITHUB_ADMIN_TOKEN` ("used
  only by reseed-github") moves into `reseed-github`'s parameters, so the agent's workflows
  never see it.
- `--var` naming a global overrides it. Any other `--var` sets a parameter of the entry
  workflow.

### Parameters

A workflow's own `vars:` are what it takes: a value is a default, `~` means required.

```yaml
name: triage-issue
vars:
  issue: ~            # required
  repo_labels: ~
  GITHUB_TOKEN: ~     # shadows the global for this call
  max_labels: 1       # optional
```

### Calls pass values

A workflow call (`workflow:`, single or `for_each`) fills the child's parameters two ways:

```yaml
- name: triage-each
  workflow: triage-issue
  for_each: "untriaged.stdout | from_json"
  as: issue
  pass: [repo_labels, GITHUB_TOKEN]   # by name, the value itself
  vars:
    max_labels: 1                     # rendered against the caller, as now
    comments: "{{ issue_comments_by_number[issue.number] }}"
```

- `pass:` is new: a list of the caller's names, handed over as they are. Not templated, so
  there is no render-to-string-and-parse-back (what made `command-inputs` 562 MB) and types are
  kept. Dotted paths are not supported in phase 2; `vars:` covers renaming and picking out.
- `vars:` stays as it is: rendered against the caller, strings coerced.

The child's starting context is: the globals; its own `vars:` defaults; what the call passes
(`pass:`, then `vars:`); and the `as` item for a `for_each`. Nothing else from the caller.

A sub-workflow's `register` stores its final context minus the globals and built-ins: what it
was passed and what it made, which is small by construction.

### Typed `for_each` steps

A step with a `type` and a `for_each` is not a call. Its iterations still read the parent's
context plus the item, so `{{ GITHUB_TOKEN }}` in a URL keeps working. Only its `register`
changes: each element is `{<as>: item, <register>: output}` (plus `failed` / `error` under
`ignore_errors`), not the iteration's whole context. The candidate's own comment ("each element
holds the issue and its listing") stays true.

### Resume

The root run's stored vars hold the globals. A sub-run's stored vars are its own starting
context, now small. On resume, a sub-run's globals are rebuilt from the root run's vars rather
than stored again with every sub-run.

## Checks

A child that reads a caller's value without being passed it now sees nothing, and an undefined
name renders as `""` rather than failing. `markov validate` (and the parse a run starts with)
reports:

- **A required parameter not passed** by a call (`~` with no `pass:` or `vars:` entry). Error.
- **A name a workflow reads that it cannot see:** not a global, a built-in, its own parameter, a
  loop variable, or set by one of its own steps. Read from templates, `when`, `for_each`,
  `for_each_when`, gate facts and assertions. Best effort (a computed key is not followed).
  Error.
- **A name passed that the child does not declare.** A warning during the migration (phases 2-4),
  an error after the sweep.
- **A step writing a global** (`register:` or `set_fact` of a global's name). Error.

## Migration

This breaks a child that reads its caller's registers, facts or undeclared inputs. The sweep:

- `examples/` (11 YAML files call workflows), the engine, parser and codify tests, the docs.
- markov-ide's samples (`DEMO_YAML`, `NEW_TEMPLATE`) and its tests that build workflows.
- github-workflow-example: declare each workflow's parameters; `GITHUB_ADMIN_TOKEN` moves into
  `reseed-github`; the cycle passes it.
- Codify's retro prompt: how scope works (a helper sub-workflow declares what it takes and the
  caller passes it, by `pass:` where it can; a global is file-level config, not a way to share
  results). Codified versions are validated with the new checks before they run, as a
  candidate's parse errors already are.
- The plan 006 trial's codified versions (v0001-v0003) are not migrated by hand: the trial
  restarts and codify writes them again under the new rules.

## Phases

1. **Typed `for_each` register:** elements are `{<as>, <register>}` (+ `failed`/`error`). This
   alone removes the trial's blowup and changes no scoping. Tests: the shape; three chained
   loops each registering stay the size of their data. Docs: for-each ("register with
   for_each"), variables-and-context.
2. **Scope:** globals from file-level vars and read-only; parameters (`~` required); `pass:`;
   `executeSubWorkflow` and the `for_each` workflow branch build the child's context as above;
   a sub-workflow `register` drops globals and built-ins; shadowing; resume rebuilds globals.
   Tests: a child sees globals, its parameters, what is passed and the item, and not the
   caller's registers; `pass:` keeps types and does not render; shadowing reaches grandchildren
   but not the caller; writing a global is rejected; a nested run resumes with its globals.
   Docs: variables-and-context ("Sub-workflow isolation", "Sub-Workflow Var Merging", the
   `register` table), workflow-file (`pass`, parameters), for-each, writing-workflows.
3. **Checks:** the four above, in `markov validate` and at parse.
4. **Sweep:** everything under Migration; then the undeclared-pass warning becomes an error.

## Open questions

- `pass:` with dotted paths (`pass: [issues.body]`) or a rename form (`pass: {labels:
  repo_labels}`): left out until a workflow needs more than `vars:` gives.
- Whether a directory workflow's per-file `vars:` (inside a workflow file, beside its
  workflows) count as globals or as defaults for those workflows only. Today they merge into
  one file-level map.

## As built (2026-10-25)

- **Phase 1:** `iterationResult` (`pkg/engine/engine.go`) keeps the item, the register and what
  the iteration set or changed (compared by identity, so nothing large is walked).
- **Phase 2:** `pkg/engine/scope.go` (`childContext`, `childResult`, `missingParams`); `pass:`
  in the parser; `checkGlobalWrites`. An entry workflow's required parameters must come from
  `--var` (or a global).
- **A workflow may change a global's name it declares in its own `vars:`:** that is its own
  parameter. The recursive loop examples (`loop-test`, `deploy-pipeline`, `pipeline-full`)
  declare their counter that way.
- **Where each check reports**, rather than all at parse: a required parameter a call does not
  give, and a step writing a global, fail the parse (they are certain). Names a workflow reads
  but cannot see, names passed that the child does not declare, and names read that callers pass
  without the child declaring them are `markov validate` errors (`parser.Lint`), and `markov run`
  logs them as warnings and goes ahead, since reading is found best effort. An entry workflow's
  undeclared reads are warnings (`--var` may set them). The migration warning for undeclared
  passes became an error once the sweep was done.
- **The check found real bugs in the examples:** `batch-rfe-pipeline`'s step type rendered
  `{{ skill }}` against the context (always empty; each step now gives its own args) and logged
  an undefined `{{ _now }}`; `deploy-pipeline`'s `health_status: "healthy"` was a set_fact
  expression evaluating to false.
- **github-workflow-example:** `github-agent` declares `sandbox: ~` and the cycle passes it;
  `GITHUB_TOKEN_FILE` is a global in `vars.yaml`. `GITHUB_ADMIN_TOKEN` stays a global: a
  secret needs a file to live in, and the only file for values is `vars.yaml` (a workflow's
  `vars:` default would put it in a workflow file). The agent's workflows can see it, but the
  sandboxed agent only gets the env its step lists. A way to keep a value in a vars file without
  making it global is still open.
- **Per-file `vars:` (open question):** each directory-workflow file holds one workflow, so its
  `vars:` are that workflow's parameters.
- **markov-ide:** `markov validate`'s findings show one diagnostic per error, at its step. Its
  samples needed no change.
- **Codify:** the retro prompt says how variables reach a called workflow; `markov validate`
  rejects a candidate that gets it wrong, with the lint lines as the reason.
