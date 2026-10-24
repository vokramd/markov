# Codify trial: github-agent-cycle (2026-10-23)

`markov codify ./github-workflow-example --workflow github-agent-cycle --fixed
github-agent-cycle,setup-openshell,reseed-github,snapshot-github --live-host github.local
--max-iterations 3 --budget-usd 2` (plan 005 phase 5). Haiku for the agent, retro and judge.

| Iteration | Retro move | Result |
|---|---|---|
| 0002 | native: fetch the issue list | rejected by the judge: the candidate's labels differ (the live run created bug and feature labels as well) |
| 0003 | native: compute the repo path and API URL from the GITHUB_ variables (set_fact), narrow the prompt | promoted v0001 |
| 0004 | native: fetch the open PR list | rejected by the judge: the live run labelled #1 "inquiry", the candidate "feature" |

About $0.47 in all (live cycles ~$0.01 each, retro $0.10–0.19, judge). Every run reseeded and
snapshotted; only workflows/github-agent.yaml changed; 0 requests recorded (github.local is
tunnelled, the agent's traffic goes through OpenShell).

Findings:
- The machinery works: fixed workflows held, live host reached for real from the cycle's
  reset and snapshot, judged comparison of snapshots, promotion into the project.
- Both rejections are agent noise, not wrong candidates: the agent's judgment differs run to
  run (triage of an ambiguous issue; which labels it creates). Comparing one candidate run with
  one live run cannot tell a worse candidate from a different roll. Options: run the baseline
  twice and treat facts that differ between baselines as noise (or accept a candidate matching
  either); make fixtures unambiguous; split triage into a focused step with a json_schema and a
  low temperature.
- The retro agent took the mechanical moves first, as designed; no split move yet (native
  extractions remained).
