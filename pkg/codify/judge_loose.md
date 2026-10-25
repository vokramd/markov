You check one step of `markov codify` in loose mode. It is rewriting the Markov workflow
`{{.Target}}` so that its agent work is done by small, atomic agent steps (each makes one
decision or produces one thing for one item) joined by deterministic native steps. Two versions
ran on the same starting state:
- {{.Reference}}: the outputs of the current version.
- {{.Candidate}}: the outputs of the proposed version.

The current version's agent steps were asked to:
{{range .Prompts}}- {{.}}
{{end}}
The proposed version's agent steps are asked to:
{{range .CandidatePrompts}}- {{.}}
{{end}}
The current version is an agent's work, and it may be incomplete or repetitive: it may have
missed items (those past the first page of a list, say) or redone work that was already done
(answered a comment that already had an answer, reviewed a PR it had already reviewed). Decide
what the job calls for from the prompts and the data, not only from what the current version
did.
{{- if .LiveFailure}}
The current version did not complete: {{.LiveFailure}}. Its outputs are partial or missing, so
they show only part of what it did. A proposed version that completes and does what the job calls
for is doing the job better; judge it against the prompts and the data.
{{- end}}
{{- if .Repeat}}
{{.Repeat.File}} records what running the workflow a second time, right after the first, changed:
{{if ge .Repeat.Live 0.0}}{{.Repeat.Live}} thing(s) for the current version, {{end}}{{.Repeat.Candidate}} for the proposed version. A
workflow that checks state before acting changes nothing on the second run.
{{- if .Repeat.Kinds}} By kind (current / proposed):
{{range .Repeat.Kinds}}- {{.Kind}}: {{.Live}} / {{.Candidate}}{{if .Touched}} (the work this step changed){{end}}
{{end}}
{{- end}}
{{- end}}

The agent work this step did not change (what the current version's broad agent still does in
both versions) varies from run to run: one run of it may redo a review or miss a reply that
another does not. Do not count such differences against the proposed version, in either run;
judge it on the work it changed and on everything else staying correct.

Agents decide some values themselves (a label they chose, the wording of a comment, the content
of a pull request), and those differ from run to run even for the same version. Do not require
them to match. Decide instead whether the proposed version does the job at least as well:
- The same actions on the items the job calls for: the items are acted on (labelled, answered,
  given a pull request, reviewed) with the same kinds of action, and nothing the current version
  did correctly is dropped. Acting on an item the job calls for that the current version missed,
  or not redoing work that was already done, is doing the job better, not a difference. Acting on
  an item the job does not call for is a difference.
- Agent-decided values are valid: within what the prompts allow (a label from the allowed set, a
  reply that answers what was asked, a pull request that addresses its issue), even if they
  differ from the current version's.
- Everything else matches: the items themselves, fixed values, structure.
Read every output file.

Write {{.Verdict}} containing a JSON object: {"equivalent": true or false, "reason": "one or
two sentences naming any difference that matters"}. Change nothing else.
