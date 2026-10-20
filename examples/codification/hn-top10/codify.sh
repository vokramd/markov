#!/usr/bin/env bash
# Runs agentic codification iterations (plan 003 phase 1). Each iteration is
# two markov processes: the task against the live API, then one codify pass
# that may promote a new task definition for the next iteration.
#
#   ./codify.sh [iterations]
#
# MARKOV_BIN selects the markov binary (default: markov on PATH, else the
# repository's bin/markov). CODIFY_DIR holds fixtures, evidence, candidates,
# versions and metrics.jsonl (default: ../hn-top10.codify, outside the
# workflow source so it never changes the source fingerprint).
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../../.." && pwd)"
iterations="${1:-1}"
markov="${MARKOV_BIN:-$(command -v markov || echo "$repo/bin/markov")}"
codify_dir="${CODIFY_DIR:-$here/../hn-top10.codify}"
mkdir -p "$codify_dir"
codify_dir="$(cd "$codify_dir" && pwd)"

done_count=$(find "$codify_dir" -maxdepth 1 -name 'iter-*' | wc -l)
for ((i = 1; i <= iterations; i++)); do
  n=$(printf '%04d' $((done_count + i)))
  it="$codify_dir/iter-$n"
  mkdir -p "$it"

  echo "== iteration $n: task"
  "$markov" run "$here" --workflow task \
    --state-store "$it/state.db" \
    --callback "jsonl://$it/task-events.jsonl" \
    --var out_dir="$it/out" || echo "task run failed; codifying anyway"

  echo "== iteration $n: codify"
  "$markov" run "$here" --workflow codify \
    --state-store "$it/state.db" \
    --callback "jsonl://$it/codify-events.jsonl" \
    --var codify_dir="$codify_dir" \
    --var iteration_dir="$it" \
    --var task_events="$it/task-events.jsonl" \
    --var project_dir="$here" \
    --var markov_bin="$markov" \
    --var markov_docs="$repo/docs/reference"

  if python3 -c 'import json,sys; sys.exit(0 if json.load(open(sys.argv[1])).get("changed") is False else 1)' "$it/retro.json" 2>/dev/null; then
    echo "retro reports nothing left to codify; stopping"
    break
  fi
done
