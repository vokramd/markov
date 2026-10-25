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
- agent_calls.md: every agent call, with the size of its rendered prompt and its input tokens. A
  call whose prompt is much bigger than the others, or carries a whole list of items, is being
  used as a work queue.
- output_sizes.md: how much each step produced, and any step that failed for producing more than
  its max_output, with the largest values inside it. previous_candidate_output_sizes.md, when
  present, is the same for the last candidate.
- history.md, when present: every earlier pass, what it tried, the mode it was judged in and
  why a candidate was rejected. previous_retro.json and previous_validation.json are the last
  pass in full. Do not repeat a move that was rejected for a reason that still applies.{{if .Loose}}
  A rejection in strict mode, or one over values an agent decided (a label it chose, its
  wording), does not apply in loose mode: such a move may be tried again.{{end}}

{{if .LiveFailure}}The current version did not complete: {{.LiveFailure}}. Fix that first, in this pass:
change the step (or what feeds it) so it produces only what is used, following the table under
"When a step produces too much" below. Its outputs are partial, so the candidate is judged on
completing and doing the job, not on matching them.

{{end}}Agent steps today: {{if .AgentSteps}}{{join .AgentSteps ", "}}{{else}}none{{end}}.

What is deterministic and what is agentic: agents are not queueing systems and are bad at math.
Given hundreds of items in one session they miss some, repeat some and miscount. So the workflow
does the listing, paging, counting, filtering, sorting, comparing with what was already done,
deciding what is pending, scheduling and retrying, and any exact matching or arithmetic. Agents
make judgments, one item per call: classify it, choose among options, write code or text, review,
decide whether two things mean the same. An agent is never the loop, the counter or the
bookkeeper. Lists in an agent's prompt are fine when they are context for its one decision (the
labels to choose from for this ticket, this issue's comments, this PR's files); a list of work
items for the agent to go through is not. A call over many items stays only when the judgment
itself spans them (deduplicate, rank, summarise), over a compact list (numbers and titles), with
the reason in the step's description.

{{if .Loose}}This run is in LOOSE mode: decompose the agent work into atomic agent steps joined by
deterministic native steps. An atomic agent step makes exactly one decision, or produces one
thing, for one item: for example "given this issue's title and body, answer with one label:
bug, feature or inquiry", or "given this issue, write the change and open a pull request".
Everything around it (fetching, finding the items, applying its answer) is native.

Each pass makes ONE coherent change: one kind of item, end to end.
1. From the evidence, list the distinct jobs the agent steps did and decide for each whether it
   needs judgment. Group them by the kind of item they serve (the issues to triage, the /mk-ping
   comments, the /mk-code requests, the PRs to review, ...), apart from the gathering they share.
2. Take ONE kind of item whose work is still done by a broad agent step, and move all of it:
   gather what it needs natively (shared with later kinds), compute natively which items are
   pending, and fan out over them with a sub-workflow per item. Within it, native steps do what
   needs no judgment (posting the fixed reply "pong"), and each judgment is an atomic claude step:
   - a short prompt for that one decision, given only the inputs it needs through template
     expressions (fetched by native steps);
   - a json_schema for its answer (a decision is an enum or a small object), so native steps
     read `<register>.structured_output` and act on it (apply the label, post the reply);
   - for_each with one call per item;
   - the model and limits that job needs; an OpenShell sandbox only when the job needs one
     (copy the sandbox, permission_mode and env params of the original step).
   Remove that kind of item from the broad prompt. Prefer the kind whose work is most mechanical
   (it is the cheapest to get right), unless a rejection that still applies says otherwise.
3. If the previous pass moved a whole kind of item and was rejected for a reason that still
   applies to a change that size, make a smaller change this pass: ONE job of that kind (the
   earliest in its data flow, or one judgment split out as in 2). Say so in the summary.
4. Leave the other kinds of item with the agent; later passes take them.
{{- if .Keep}}
5. These steps must stay agent steps: {{join .Keep ", "}}. You may narrow their prompts.
{{- end}}

Shape the workflow as gather → compute what is pending → fan out:
- Gather lists completely: page with http_request's paginate, and assert the list is complete
  (`assert: "not issues.truncated"`). A list read as one page silently misses everything after it.
- Check state before every action. Each write is a for_each over a list computed natively from
  what is wanted and what already exists, e.g. the /mk-ping comments with no later reply from the
  bot, or the issues with no triage label. Running the workflow a second time right after the
  first should find nothing left to do and change nothing.
- One sub-workflow per kind of item, named for its job (workflows/{{.HelperPrefix}}triage-issue,
  {{.HelperPrefix}}answer-ping, {{.HelperPrefix}}review-pr, ...). It takes one item, checks that
  item's own state, makes at most one agent call for it, and acts on the answer; the main workflow
  calls it with for_each over the pending items.
- An agent prompt that hands over a list of work items (all issues, every comment of every open
  item) becomes such a fan-out; give each call only its item's data.
{{- if .RepeatChanges}}
- The run records what a second run changed in {{.RepeatChanges}} (in outputs/), counted by
  kind of change beside the total (comments_added, labels_added, reviews_added, ...). Name in
  retro.json "repeat_kinds" every kind of change the work you moved makes (answering pings:
  comments_added). The candidate may not repeat more of those kinds than the current version;
  aim for none. Repeats of other kinds come from work you left with the agent, which varies from
  run to run; they do not fail the pass on their own.
{{- end}}

The values an atomic step decides (which label, what wording) may differ from run to run; that
is expected and is not a reason to keep work with a broad agent. Do not answer "changed": false
while an agent step still does more than one judgment job: such a pass counts as failed.

{{else}}Codification is gradual. Each pass makes exactly ONE change:
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

{{end}}Prefer native steps (http_request, for_each, set_fact, write_file, assert, sub-workflows) over
scripts. Use a script under {{.ScriptsDir}} with script_exec only for what the step types and
templates cannot reasonably express, and say why in retro.json.

How variables reach a workflow (`markov validate` rejects a candidate that gets this wrong):
- A called workflow sees only the file-level vars (globals), the markov_* built-ins, its own
  `vars:` and what the call gives it. Not the caller's registers or facts. Declare what a helper
  takes in its `vars:` (`~` for required) and pass it from the caller: `pass: [name]` hands over
  a register or list as it is; `vars:` renders, so use it to pick out one item's part
  (`comments: "{{"{{"}} comments_by_number[issue.number] {{"}}"}}"`), not to copy a whole register.
- No step may `register`, `set_fact` or loop `as` the name of a file-level var.
- A typed step's for_each register holds, per item, the item and that step's output; filter or
  reshape it with set_fact or a script before handing it on, rather than bundling registers.

When a step produces too much (output_sizes.md: over its max_output, or a large share of it, or
growing with the data):
| The evidence shows | Usually |
|---|---|
| A register held inside another register's items | pass only the fields needed (`pass:`, or `vars:` with `map(attribute=...)`), not the whole register |
| A big list rendered into a set_fact or template | write it to a file and have a script_exec read it: the script filters, and the workflow gets back only its result |
| A big list given to an agent | fan out with one call per item, or give the agent a file to read |
| A listing much bigger than what is used | filter on the server (`state=open`, `since=`, `labels=`) or fetch per item |
| One huge text field (a log, a transcript) | save it as an artifact and register its path or a summary |
A step may set a larger `max_output:` only when the size is the job's own (a listing that really
is that big), with the reason in a comment.

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
{{- if .Loose}}
- How the candidate is judged: it runs on the same starting state, and its outputs are compared
  with the outputs above for the same actions on the same items; values agents decide only need
  to be valid. Every claude step you give a json_schema must answer in it on every call.
{{else}}
- How the candidate is judged: it runs against the recorded requests and its outputs are
  compared with the outputs above. {{if .Judged}}While an agent produces an output, the
  comparison is about content (the same items, values, order and links), not formatting, so
  the first native version of an output may fix its format.{{else}}The current version has no
  agent steps, so the outputs must be identical.{{end}} A request that is not in the recording
  fails, and only GET and HEAD are replayed.
{{end}}
- Markov's reference docs are in {{.Docs}}: read step-types.md, for-each.md (especially
  "register with for_each"), template-engine.md, variables-and-context.md and
  workflow-file.md before writing steps. schema.json lists every step type and parameter.
- Check the candidate with: {{.Markov}} validate .
  Do not run the workflow.
- If nothing is left to codify (no agent steps remain, no mechanical job is left with an agent,
  and every agent step does a single job that needs judgment{{if .Loose}}: is atomic{{end}}),
  change nothing.

Finally write {{.RetroJSON}} containing a JSON object with keys:
- "changed": true or false
- "jobs": the jobs you listed in step 1, each an object with "job", "judgment" (true or false)
  and "where" after this pass ("native", "script", "agent" for the broad step, or
  "focused:<workflow>.<step>")
- "extracted": the one job this pass moved out of the agent, or null
- "move": "native", "script" or "split" (a focused agent step), or null when nothing changed
- "summary": one or two sentences on what you changed and why
- "script_justification": why a script was needed, or null when you added none{{if .RepeatChanges}}
- "repeat_kinds": the kinds of change (keys beside the total in the repeat record) that the
  work you moved makes, or [] when the pass moved none{{end}}
- "remaining_agent_steps": the number of claude steps in the workflows above after this pass
