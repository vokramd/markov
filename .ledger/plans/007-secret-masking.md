# Secrets masked in events and logs

Created 2026-10-24. Comes out of plan 006: a bootstrap step that mints the bot's GitHub token
needs somewhere to put it that is not the run log.

Markov writes values out in plain text. `run_started` carries every var (`GITHUB_TOKEN`,
`GITHUB_ADMIN_TOKEN`, `ANTHROPIC_API_KEY` from the github-workflow-example vars), step outputs
carry whatever a step returned, and `--verbose` logs registered outputs and set_fact values. The
event log is read by people, by markovd and the IDE through callbacks, and by `markov codify`,
whose evidence goes to the retro agent, so a token in an event reaches the model too. The only
protection is `executor.SecretParams`, which hides the `credentials` param of
`openshell_provider` in verbose logs and nothing else.

**Status:** phase 1 done (2026-10-24).

## Approach

**Mask by value, at the places values leave the engine.** The engine keeps the run's secret
values; every string in an event, and every log line, has each one replaced with
`<secret:NAME>` (as GitHub Actions' `add-mask` does). Masking by value catches a secret wherever
it ends up: a rendered URL or header, shell stdout, an error message, an agent's transcript, a
sub-workflow var with another name. Steps still get the real values.

**Which values are secret:**

- **Declared vars.** A file-level `secrets:` list of var names (`meta.yaml` in a directory
  workflow).
- **Vars named like secrets.** A var whose name ends in `token`, `secret`, `password`,
  `passwd`, `api_key`/`apikey`, `private_key` or `credential(s)` (any case) is secret without
  being listed, so a forgotten entry is not a leak.
- **Step outputs.** `secret: true` on a step marks every string in its output; `secret: [token,
  body.token]` marks those paths. The values are added before the step's events fire, so the
  step that mints a token never shows it. On `set_fact` the paths are fact names.
- **Secret step params.** The rendered values of `SecretParams` (`openshell_provider`
  credentials) are added when the step runs.

Strings under 8 characters are not masked (a secret `1` would mask every `1`). A value is masked
as it is, trimmed, and URL-query-escaped. Map keys are masked as well as values. The set only
grows during a process: masking a value that stopped being secret is harmless.

**Where:** `fireEvent` hands every callback (JSONL, HTTP, gRPC, so markovd and the IDE; debugger
events go the same way) a masked copy of the event; the run's own context is never changed. The
CLI sends the standard logger through the same masker. `--debug`'s flag dump lists `--var` names,
not values.

**Not the state store.** Resume rebuilds the run's context from the stored vars and step outputs,
so the store keeps real values (phase 3 looks at this). On resume the masker is rebuilt from the
same sources: the stored vars, and the outputs of completed steps marked `secret`.

**Not covered:** a value in another form (base64, a substring, split across lines), and anything
a step sends somewhere itself (an agent posting a token to GitHub).

## Phases

1. **Masking events and logs** (done; checked end to end with `bin/markov`, a JSONL log and
   `--verbose --debug`: no declared, secret-named or minted value in either): `pkg/secret` (the masker, a masking `io.Writer`); parser
   `secrets:` (file and `meta.yaml`) and step `secret:` (true or a list of paths; rejected on
   `workflow`, `gate` and `assert` steps, which have no output of their own); the engine adds
   declared and secret-named vars at run, sub-run and resume start, secret step outputs and
   secret params as steps run; `fireEvent` masks; the CLI masks its log output and stops dumping
   `--var` values. Tests: masking rules (short values, longest first, keys, nested values,
   escaped forms), a run whose events and log hold no secret var or secret step output, resume.
   Docs: workflow-file, variables-and-context, step-types, callbacks.
2. **Use it:** github-workflow-example declares its secrets; the plan 006 bootstrap step marks
   its minted token `secret`; codify evidence checked for leaks after a trial.
3. **The store:** decide whether stored vars and outputs should hold masked values, with secret
   vars supplied again on resume (and secret step outputs re-run), or stay as they are with the
   database treated as sensitive. markovd's run views read the store.
