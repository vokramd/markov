#!/usr/bin/env bash
# Runs the agentic codification loop (plan 003) in one markov process: the
# codify_loop workflow runs the task, lets the retro agent codify one job,
# promotes a validated candidate and reloads itself until nothing is left.
#
#   ./codify.sh [max_iterations]
#
# MARKOV_BIN selects the markov binary (default: markov on PATH, else the
# repository's bin/markov). CODIFY_DIR holds fixtures, evidence, candidates,
# versions, metrics.jsonl and the loop's events.jsonl (default:
# ../hn-top10.codify, outside the workflow source).
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(cd "$here/../../.." && pwd)"
markov="${MARKOV_BIN:-$(command -v markov || echo "$repo/bin/markov")}"
codify_dir="${CODIFY_DIR:-$here/../hn-top10.codify}"
mkdir -p "$codify_dir"
codify_dir="$(cd "$codify_dir" && pwd)"

cd "$here"
exec "$markov" run . --workflow codify_loop --allow-reload \
  --state-store "$codify_dir/state.db" \
  --callback "jsonl://$codify_dir/events.jsonl" \
  --var codify_dir="$codify_dir" \
  --var markov_bin="$markov" \
  --var markov_docs="$repo/docs/reference" \
  --var max_iterations="${1:-8}"
