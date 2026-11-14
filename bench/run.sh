#!/usr/bin/env bash
# Run bench tasks headless and check them: bench/run.sh [task ...]  (no args = all)
# Env: AICODE (binary), AICODE_CONFIG (config file), extra flags after "--" go to aicode.
set -u
DIR=$(cd "$(dirname "$0")" && pwd)
BIN=${AICODE:-$DIR/../aicode}
CFG=${AICODE_CONFIG:-$DIR/config.yml}
tasks=()
extra=()
while [ $# -gt 0 ]; do
  if [ "$1" = "--" ]; then shift; extra=("$@"); break; fi
  tasks+=("$1"); shift
done
[ ${#tasks[@]} -eq 0 ] && tasks=($(ls "$DIR/tasks"))
pass=0
for t in "${tasks[@]}"; do
  run=$(mktemp -d "${TMPDIR:-/tmp}/aicode-bench-$t-XXXX")
  "$DIR/workspace.sh" "$t" "$run/ws"
  start=$(date +%s)
  (cd "$run/ws" && "$BIN" -config "$CFG" ${extra[@]+"${extra[@]}"} -q "$(cat "$DIR/tasks/$t/task.md")" </dev/null >"$run/answer" 2>"$run/log")
  secs=$(( $(date +%s) - start ))
  result=$(python3 "$DIR/check.py" "$t" "$run/ws" "$run/answer") && pass=$((pass + 1))
  printf '%-15s %s  (%ss · %s)\n    %s\n' "$t" "$result" "$secs" "$(tail -1 "$run/log")" "$run"
done
echo "passed $pass/${#tasks[@]}"
