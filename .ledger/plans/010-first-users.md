# First users

Created 2026-11-06. Users, not customers: people who run markov on their own work, come back,
and tell us where it broke. Paying for markovd or Markov IDE comes later, from people the engine
already serves.

**Status:** phase 1 done (2026-11-07): v0.1.0 released (https://github.com/vokramd/markov/releases/tag/v0.1.0)
with static linux and darwin, amd64 and arm64 binaries from goreleaser on the tag; CI (fmt, vet, test, static build)
on main and PRs; `markov --version`; the module moved to `github.com/vokramd/markov` (markovd too), so
`go install github.com/vokramd/markov/cmd/markov@latest` works; `examples/quickstart.yaml` and a README Install /
Quick Start that need no cluster (`TestQuickstartExampleRuns`); descriptions and topics on markov, markovd and
markov-harness, Discussions on markov; Apache-2.0 in markov-harness. On the way, the GitHub Pages build of docs/,
broken since 2026-10-10, was fixed. Phase 2, the Codify tutorial, is next.

## Where we are

Checked on GitHub, 2026-11-06:

- Stars: markov 5, markovd 1, markov-harness 1. No forks, no issues. There has been no launch, so
  this is a start, not a failed launch.
- **No releases.** Trying markov means cloning and `go build`.
- **The README Quick Start leads with `examples/k8s-job-test.yaml`**, which needs a cluster.
- **The repos have no description or topics**, so GitHub search and topic pages don't find them.
- The site (markovd.com) and the markov README now lead with "chain agents, codify what works",
  and markov-harness has a demo GIF.

## Who first

People already chaining Claude Code skills or agent calls by hand or in scripts, who feel it is
fragile: no state, no resume, no way to tell which part needed the model. They have Claude Code,
not a Kubernetes cluster. markov answers them with workflows that resume, loop and debug, and
Codify answers the part no other tool does: moving the work that needs no judgment out of the agent.

Not first: platform teams shopping for a Kubernetes workflow engine (Argo, Tekton are the default),
and markovd or IDE buyers.

## Approach

### 1. The first five minutes work (before any promotion)

Anyone who arrives from a post must get from the README to a run on a laptop without a cluster,
a Go toolchain or a guess.

- **Releases**: goreleaser for linux/darwin × amd64/arm64 binaries on tags, `go install
  github.com/vokramd/markov/cmd/markov@latest` documented (the module moved from `jctanner` to
  `vokramd` on 2026-11-07, before the first release), and a Homebrew tap later.
- **Quick Start on a laptop**: lead with an example that needs only `markov` (and `claude` for the
  agent one), runs in under a minute, and prints something worth reading. Kubernetes moves to its
  own section.
- **GitHub metadata**: a description and topics (`ai-agents`, `claude-code`, `workflow-engine`,
  `llm`, `agentic-workflows`) on markov, markovd and markov-harness. Turn on Discussions on markov.
- **markov-harness**: it has no license; add one (Apache-2.0, like the others) before pointing
  people at it.

### 2. A first win that matches the pitch

One tutorial, `docs/guides/codify-your-first-workflow.md`: start from three Claude Code skills a
reader would run by hand, wire them into a workflow, run it, then run `markov codify` and watch one
job move from the agent into native steps. Use a task people have (GitHub issue triage,
summarizing a changelog, a release note), with the repo's GitHub emulator or a public repo so
it runs without anyone's credentials. A short recording of it, like markov-harness's.

### 3. Launch with the story, spaced out

The headline is the story, not the product. In order, a few days apart, fixing what each wave runs
into before the next:

1. **Show HN**: "I rebuilt a minimal Claude Code as a YAML workflow" → markov-harness, GIF first.
   Stay in the comments for the first hours.
2. **Claude Code communities**: r/ClaudeAI, Claude Code Discords, r/LocalLLaMA, where people hit
   the fragile-chain problem.
3. **An essay on the idea**: progressive crystallization; agents make one judgment per item, the
   workflow queues, counts and tracks the rest. Ideas travel further than tools, and every later
   link has context.

### 4. Meet people where they work

- **A Claude Code plugin or skill** that writes a markov workflow from a description or from a
  skill chain someone already runs, so markov is found from inside Claude Code.
- **Answer fragile-workflow posts with the workflow**: when someone shares a brittle multi-agent
  script, reply with it as a markov workflow (resume, loops, debugger). Specific help, not an ad.

### 5. The first ten users by hand

Find people struggling with agent orchestration in public (issues on LangGraph or CrewAI, blog
posts, threads), offer to convert one of their workflows, watch them use it, and fix every place
they get stuck. Ten people who rely on it teach more than a thousand stars.

## Phases

1. **Ready**: releases, laptop Quick Start, GitHub metadata and Discussions, markov-harness license.
2. **First win**: the Codify tutorial and its recording.
3. **Launch**: Show HN, communities, the essay, a few days apart.
4. **In the tools**: the Claude Code plugin or skill.
5. **Ten users**: by hand, alongside 3 and 4, and continuing.

## Measures

Not stars. Count:

- People who ran it: issues and Discussions from people outside the project, release downloads.
- People who came back: a second issue, a workflow of their own shared, a PR.
- Where they got stuck: each one a task, fixed before the next launch wave.

## Open questions

- ~~Move the Go module paths from `github.com/jctanner/...` to `github.com/vokramd/...` before the
  first release?~~ Done 2026-11-07, for markov and markovd.
- Which task the tutorial codifies: one that shows a real Codify win on a recorded run, and also
  runs on a reader's machine without credentials.
- Whether markovd and Markov IDE appear in launch posts at all, or only on the site.
