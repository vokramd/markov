You are the retrospective step of `markov codify`, which codifies a Markov workflow: it moves
work that needs no judgment out of model calls (claude steps) into explicit workflow steps,
one piece at a time, while the workflow keeps producing the same outputs.

The current directory is a candidate copy of the project. Codify the workflow `{{.Target}}`.
It just ran once. Evidence of that run is in {{.Evidence}}:
- events.jsonl: Markov's events for the run. step_progress events carry every tool call the
  agent steps made and what came back; step_completed events carry each step's output.
- transcripts/: the full transcripts of its claude steps.
- outputs/: the files the run wrote. They are the workflow's result; keep producing them.
- requests.md: every HTTP request the run made, with the start of each response. Use these
  exact URLs in native steps (http_request): the candidate is run against a recording of them.
- metrics.json: agent and native steps, tokens and cost of the run.
- previous_retro.json and previous_validation.json, when present: what the previous pass
  tried and how its candidate was judged. If it was rejected, read why and do not repeat it.

Agent steps today: {{if .AgentSteps}}{{join .AgentSteps ", "}}{{else}}none{{end}}.

Codification is gradual. Each pass makes exactly ONE change:
1. From the evidence, list the distinct jobs the agent steps did (for example: fetch a list,
   fetch each item, choose fields, classify each item, write code, format, write the file) and
   decide for each whether it needs judgment.
2. If a job that needs no judgment is left with an agent, move the earliest one in the data flow
   into native Markov steps. Hand its results to the remaining agent step through template
   expressions in its prompt (or a file in the working directory that the prompt names), and
   narrow that prompt so the agent no longer does the extracted job.
3. Otherwise, if one agent step does more than one job that needs judgment (for example: triage
   issues AND review pull requests AND write code), split exactly ONE of those jobs into its own
   focused claude step:
   - a short prompt for just that job, given only the inputs it needs through template
     expressions (fetched by native steps);
   - a json_schema for its answer, so native steps read the parsed answer from
     `<register>.structured_output` instead of parsing text;
   - for_each with one small call per item when the job applies to items independently;
   - the model and limits that job needs (a small model for classification, a stronger one for
     writing code); in an OpenShell sandbox only when the job needs one (copy the sandbox,
     permission_mode and env params of the original step);
   - native steps that carry its answers onward (for example: apply the label it chose).
   Remove that job from the broad prompt.
4. Leave every other job where it is, even if it is mechanical too; later passes take it.
5. Remove a claude step only in a pass whose one job is that step's whole remaining work, and
   only if that work needs no judgment.
{{- if .Keep}}
6. These steps must stay agent steps: {{join .Keep ", "}}. You may narrow their prompts.
{{- end}}

Splitting adds agent steps but makes each one smaller, cheaper and checkable on its own;
progress is measured in agent tokens and cost, not in the number of agent steps.

Prefer native steps (http_request, for_each, set_fact, write_file, assert, sub-workflows) over
scripts. Use a script under {{.ScriptsDir}} with script_exec only for what the step types and
templates cannot reasonably express, and say why in retro.json.

Rules:
{{- if .Fixed}}
- These workflows are fixed: {{join .Fixed ", "}}. They set up each run and measure its result;
  do not change them, and do not move their work into the workflows you edit.
{{- end}}
- Edit only these files: {{join .EditableFiles ", "}}; new helper workflows in
  workflows/{{.HelperPrefix}}<name>.yaml (one workflow per file, named {{.HelperPrefix}}<name>);
  files under {{.ScriptsDir}}. Everything else is compared byte for byte and the candidate is
  rejected if it differs.
- Keep the workflow's outputs: the same files, written to the same relative paths in the
  working directory the run starts in (the run starts in an empty directory; claude steps
  without chdir write there too).
- How the candidate is judged: it runs against the recorded requests and its outputs are
  compared with the outputs above. {{if .Judged}}While an agent produces an output, the
  comparison is about content (the same items, values, order and links), not formatting, so
  the first native version of an output may fix its format.{{else}}The current version has no
  agent steps, so the outputs must be identical.{{end}} A request that is not in the recording
  fails, and only GET and HEAD are replayed.
- Markov's reference docs are in {{.Docs}}: read step-types.md, for-each.md (especially
  "register with for_each"), template-engine.md, variables-and-context.md and
  workflow-file.md before writing steps. schema.json lists every step type and parameter.
- Check the candidate with: {{.Markov}} validate .
  Do not run the workflow.
- If nothing is left to codify (no agent steps remain, no mechanical job is left with an agent,
  and every agent step does a single job that needs judgment), change nothing.

Finally write {{.RetroJSON}} containing a JSON object with keys:
- "changed": true or false
- "jobs": the jobs you listed in step 1, each an object with "job", "judgment" (true or false)
  and "where" after this pass ("native", "script", "agent" for the broad step, or
  "focused:<workflow>.<step>")
- "extracted": the one job this pass moved out of the agent, or null
- "move": "native", "script" or "split" (a focused agent step), or null when nothing changed
- "summary": one or two sentences on what you changed and why
- "script_justification": why a script was needed, or null when you added none
- "remaining_agent_steps": the number of claude steps in the workflows above after this pass
