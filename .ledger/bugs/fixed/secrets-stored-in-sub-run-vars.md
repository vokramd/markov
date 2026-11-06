# Bug: secrets passed to sub-workflows are stored in plain text with every sub-run

## Status

Fixed 2026-11-06.

- `executeSubWorkflow` and the `for_each` sub-workflow path store a sub-run's vars through
  `subRunVarsJSON` (`pkg/engine/secrets.go`): the masker's copy, `<secret:NAME>` in place of each
  secret, JSON-encoded without HTML escaping so the marker stays readable.
- Nothing reads a sub-run's `VarsJSON` back: `Resume` decodes only the top-level run's vars and
  rebuilds sub-runs' vars from the parent's context. So the masked copy loses nothing.
- Docs: the secrets limits in `docs/reference/variables-and-context.md` now say which stored
  values stay real (top-level vars, `secret` step outputs) and that sub-run vars are masked.

Test: `TestSecretsAreMaskedInStoredSubRunVars` (a declared secret and a `secret: [token]` step
output passed to a sub-workflow and to two `for_each` items). It failed without the fix (all
three sub-run rows held both values) and checks each sub-run's step still got the real value.
Checked with markov-harness: a new session's 6 sub-run rows hold no copy of the relay key, and
the 4 that are passed it show `<secret:...>`.

## Summary

Masking covered events and log lines, but `state.Run.VarsJSON` for each sub-run was
`json.Marshal(subVars)`. A workflow that threads a secret through recursive calls, as
markov-harness does with its relay API key (`turn` → `agent_step` → `continue` ...), stored a
fresh plain-text copy with every call.

## Reproduction

markov-harness `.state/container/markov-state.db` (2026-11-06): 920 of 1,299 rows in `runs`
had the relay key in `vars_json`, none in `steps`. The key comes from a `load_artifact` step with
`secret: true` and is passed to each sub-workflow as `relay_api_key`.

## Not changed

The top-level run's vars and completed `secret` steps' outputs are still stored in plain text;
resume needs them. Rows written before the fix keep their values.
