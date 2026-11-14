#!/usr/bin/env bash
# Copy a task's starting files to <dest>: bench/workspace.sh <task> <dest>
set -eu
DIR=$(cd "$(dirname "$0")" && pwd)
t=$1 dest=$2
fixture=$(python3 -c "import json,sys; print(json.load(open(sys.argv[1])).get('fixture',''))" "$DIR/tasks/$t/meta.json")
if [ -n "$fixture" ]; then
  cp -R "$DIR/fixtures/$fixture" "$dest"
else
  cp -R "$DIR/tasks/$t/workspace" "$dest"
fi
if [ -d "$DIR/tasks/$t/overlay" ]; then
  cp -R "$DIR/tasks/$t/overlay/." "$dest/"
fi
