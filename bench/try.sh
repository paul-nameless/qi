#!/usr/bin/env bash
# Open a task interactively in a fresh copy: bench/try.sh <task>
set -eu
DIR=$(cd "$(dirname "$0")" && pwd)
t=${1:?usage: bench/try.sh <task>   (tasks: $(ls "$DIR/tasks" | tr '\n' ' '))}
BIN=${AICODE:-$DIR/../aicode}
CFG=${AICODE_CONFIG:-$DIR/config.yml}
run=$(mktemp -d "${TMPDIR:-/tmp}/aicode-try-$t-XXXX")
"$DIR/workspace.sh" "$t" "$run/ws"
echo "task: $(cat "$DIR/tasks/$t/task.md")"
echo "dir:  $run/ws"
echo "check afterwards: python3 $DIR/check.py $t $run/ws <answer-file>"
cd "$run/ws" && exec "$BIN" -config "$CFG"
