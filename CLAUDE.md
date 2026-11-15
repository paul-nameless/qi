# aicode

Minimal Go coding agent for the terminal (v2 rewrite). Plan: `docs/plans/aicode-v2.md` (docs/ is gitignored, local only).

## Layout
- `main.go` flags and wiring, `repl.go` interactive loop, `commands.go` slash commands and image attachment, `picker.go` the `/` picker (runs the `picker` config command, fzf by default), `filelist.go` the `@` file list (git files first, then a capped breadth-first walk), `external.go` the shared contract for external UI tools (`toolAvailable`, `expandTool` turns placeholders into quote-aware `$AICODE_*` env references so values never become shell code, `choose`, `runQuiet`, `runTTY` via `Editor.RunTTY`)
- `internal/llm` canonical message types, SSE, provider adapters (`openai.go` chat completions incl. OpenRouter, `anthropic.go`)
- `internal/agent` turn loop, `frontmatter.go` minimal SKILL.md frontmatter parser (top-level scalars only, whitespace-normalised), `builtin.go` embedded built-in skills (`builtin/aicode-config`) written with the schema to the user cache dir and listed in the static prompt, compaction (`compact.go`: unchanged-history summary request, verbatim tail without reasoning between turns, summary-only mid-turn, overflow retry, thrash pause), steering queue, `images.go` evicts the oldest images past 100 or 24 MB (never on a normal request), system prompt (`prompt.go` records named sections for `/context`, `context.go` the breakdown), skills, subagent runner, hooks, project trust
- `internal/config` JSON config with providers and profiles (`decode.go` strict, case-sensitive decoding with encoding/json/v2, file/key errors and the YAML migration hint; `validate.go` the schema's enums, minimums and required fields; `schema.json` the embedded JSON Schema, kept in sync with the structs by a test); `mcp.go` the tool globs and the per-profile `mcp` merge by server name (null removes a global server), checked after merging; `limits.go` parses `compact_at`/`compact_idle_at` (tokens or %) and resolves them against `context_window` or a small model table
- `internal/tools` registry with parallel execution, read/write/edit/bash/subagent, read-only allowlist, `files.go` tracks file mtimes so write refuses to overwrite a file changed since the model last read or wrote it (edit needs no guard: it only replaces exact text); it also snapshots files the model read or wrote, and the agent sends a diff of any outside change with the next tool results or user message; `image.go` scales images from read, paste and MCP to ≤1568px with a stdlib area-average resize, `read` takes `crop` for full-resolution regions
- `internal/ui` termios, key decoder, line editor, printer with status line, `markdown.go` streaming markdown styling of answers (TTY only; `table.go` aligns tables, `width.go` display width)
- `internal/session` append-only JSONL sessions; `Load`/`Entries` are read-only (`Open` repairs the file for appending), `List` summarizes a directory's sessions
- `sessions.go` `/resume`, `-r`, `-show`; `render.go` renders a session as text (plain for `-show`, colored with `ui.Markdown` for the `/history` pager)
- `internal/mcp` stdio MCP client (lazy start, tool search, per-server `tools`/`exclude_tools` globs from `config/mcp.go` enforced in the Manager: hidden tools never listed or called); exposed as `tool_search`/`mcp_call` in `internal/tools/mcp.go` (compact listing past 10 tools, never cut by the output cap), `/mcp` in `commands.go`; subagents get the parent's `mcp` as JSON in `AICODE_MCP` (unset on read, replaces the config's); `mcptest` is the fake server the tests run

## Commands
- Build: `go build -o aicode .`
- Test: `go test ./...`
- Format, vet, lint, test: `task ch` (lint runs golangci-lint v2.15.0 via `go run`, same as CI)
- Headless smoke test: `./aicode -q "prompt" </dev/null` (uses `~/.config/aicode/config.json`); `./aicode -check-config [-config f] [-p name]` validates config without calling the API; to trigger compaction, run with `-config` pointing at a copy of the config with a small `compact_at` (e.g. `20000` or `1.5%`); the system prompt and tools alone are ~5k tokens, so lower values pause auto-compaction
- Opus/Anthropic: `./aicode -p opus` (native API, key from `pass`); prefer luna for routine checks, it is much cheaper
- Replaying real tasks against Claude Code sessions: `~/Documents/code/pro/aicode-bench/replay.sh` (isolated repo copies, see its readme)
- UI testing: run in tmux (`tmux new-session -d -s ai -x 120 -y 40 './aicode'`, then `send-keys`/`capture-pane`); shift+tab is `$'\e[Z'`, bracketed paste via `tmux paste-buffer -p`

## Rules
- Keep the request prefix byte-stable for prompt caching: no timestamps in the system prompt, fixed tool list (read-only is enforced at execution, not by removing tools), deterministic JSON. The system prompt is two cached blocks: the static part (base prompt, global instruction files, global skills) must not depend on the directory, so every session shares it; cwd, date and project files go in the second block
- Every `tool_use` in the log needs a `tool_result`; append assistant messages only when the stream completes
- Compaction must not rewrite the history it sends (cache); messages kept after a summary carry no reasoning blocks (Claude 5.x rejects replayed thinking after a changed prefix)
- Never `MakeRaw` the terminal: it clears OPOST and output staircases; `ui.Terminal` keeps ISIG so ctrl+c is SIGINT
- No code comments
- Small, focused functions; check errors and return meaningful messages
- External UI tools (picker, pager, notifier, ...) are shell-string config keys with `{placeholders}`, never hard dependencies, documented in readme "External tools"; plan in `docs/plans/external-tools.md`. Defaults live in `config.toolDefaults`; add new keys to `repl.toolKeys` so `/config` lists them
- Only the user's input and the model's answer print in normal color; tool lines, reasoning and info are dim
