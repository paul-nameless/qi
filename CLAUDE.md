# aicode

Minimal Go coding agent for the terminal (v2 rewrite). Plan: `docs/plans/aicode-v2.md` (docs/ is gitignored, local only).

## Layout
- `main.go` flags and wiring, `repl.go` interactive loop, `commands.go` slash commands and image attachment
- `internal/llm` canonical message types, SSE, provider adapters (`openai.go` chat completions incl. OpenRouter, `anthropic.go`)
- `internal/agent` turn loop, compaction, steering queue, system prompt, skills, subagent runner, hooks, project trust
- `internal/config` YAML config with providers and profiles
- `internal/tools` registry with parallel execution, read/write/edit/bash/subagent, read-only allowlist
- `internal/ui` termios, key decoder, line editor, printer with status line
- `internal/session` append-only JSONL sessions
- `internal/mcp` stdio MCP client (lazy start, tool search); exposed as `tool_search`/`mcp_call` in `internal/tools/mcp.go`

## Commands
- Build: `go build -o aicode .`
- Test: `go test ./...`
- Format, vet, lint, test: `task ch` (lint runs golangci-lint v2.15.0 via `go run`, same as CI)
- Headless smoke test: `./aicode -q "prompt" </dev/null` (uses `~/.config/aicode/config.yml`)
- UI testing: run in tmux (`tmux new-session -d -s ai -x 120 -y 40 './aicode'`, then `send-keys`/`capture-pane`); shift+tab is `$'\e[Z'`, bracketed paste via `tmux paste-buffer -p`

## Rules
- Two deps only (`golang.org/x/sys`, `goccy/go-yaml`); no SDKs, no TUI libraries
- Keep the request prefix byte-stable for prompt caching: no timestamps in the system prompt, fixed tool list (read-only is enforced at execution, not by removing tools), deterministic JSON
- Every `tool_use` in the log needs a `tool_result`; append assistant messages only when the stream completes
- Never `MakeRaw` the terminal: it clears OPOST and output staircases; `ui.Terminal` keeps ISIG so ctrl+c is SIGINT
- No code comments
- Small, focused functions; check errors and return meaningful messages
