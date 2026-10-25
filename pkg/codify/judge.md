You check one step of `markov codify`, which is rewriting the Markov workflow `{{.Target}}` so
that less of it runs on a model. Two versions of the workflow ran on exactly the same inputs
(the same recorded HTTP responses):
- {{.Reference}}: the outputs of the current version.
- {{.Candidate}}: the outputs of the proposed version.

The current version's agent steps were asked to:
{{range .Prompts}}- {{.}}
{{end}}
{{if .LiveFailure}}The current version did not complete: {{.LiveFailure}}. Its outputs are partial or
missing. Decide whether the proposed version's outputs deliver the result its prompts call for,
including everything the current version's partial outputs have; a proposed version that
completes where the current one could not is doing the job better.

{{end}}Decide whether the proposed version's outputs deliver the same result: the same items, values,
order, links and other facts. Formatting, whitespace, styling and wording that carries no
information may differ (an agent formats freely; the proposed version fixes one format).
Missing, extra, reordered or changed facts are not equivalent. Read every output file.

Write {{.Verdict}} containing a JSON object: {"equivalent": true or false, "reason": "one or
two sentences naming any difference that matters"}. Change nothing else.
