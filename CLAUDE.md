# qi

Minimal Go coding agent for the terminal. The model's only tool is `run`: JavaScript in qi's own engine (`internal/jsvm`) calling async functions (read, write, edit, bash, jobs, subagent, tool_search, mcp).

`main.go` holds only `var version` (stamped by goreleaser's `-X main.version`) and `os.Exit(app.Main(os.Args[1:], version))`; everything else is under `internal/`.

## Commands

- Build: `go build -o qi .`
- Test: `go test ./...`; `task test` runs every test uncached and fails when the suite takes 5s or more (local budget, ~4s now)
- Race: `go test -race ./...` is what CI runs (plus gofmt, vet, golangci-lint, bwrap installed for the Linux sandbox); `task ch` does not run `-race`, so run it yourself after concurrency changes
- Format, vet, lint, test: `task ch` (fmt and lint in parallel, then `task test`; lint runs golangci-lint v2.15.0 via `go run`, same as CI)
- Golden requests: `go test ./internal/app -run TestRequestBodiesPinned -update` rewrites `internal/app/testdata/request-*.json`; only update when the request change is intended, and read the diff
- Headless smoke test: `./qi -q "prompt" </dev/null` (uses `~/.config/qi/config.json`); `./qi -check-config [-config f] [-p name]` validates config without calling the API
- Compaction by hand: `-config` with a copy of the config and a small `compact_at` (`20000` or `1.5%`); prompt and tools alone are ~5k tokens, so lower values pause auto-compaction
- UI testing: run in tmux (`tmux new-session -d -s ai -x 120 -y 40 './qi'`, then `send-keys`/`capture-pane`); shift+tab is `$'\e[Z'`, bracketed paste via `tmux paste-buffer -p`
- `internal/jsvm/bench` is its own Go module (own `go.mod`), so `go test ./...` skips it

## Rules

- Keep the request prefix byte-stable for prompt caching: fixed tool list (modes are enforced at execution, never by removing tools), deterministic JSON
- Nothing time-dependent in the cached system blocks, nothing directory-dependent in the static block
- The system prompt has three parts (`agent.BuildSystemPrompt`, split by `llm.Request.SystemParts`):
  - cached static block: base prompt, global instruction files, global and builtin skills; directory-free, so every session shares it
  - cached project block: project instruction files and skills
  - uncached `<env>` tail: cwd, scratch dir, platform, date, git (the Responses API sends all three as one `instructions` string)
- Every `tool_use` in the log needs a `tool_result`; append assistant messages only when the stream completes
- Compaction must not rewrite the history it sends (cache); messages kept after a summary carry no reasoning blocks (Claude 5.x rejects replayed thinking after a changed prefix)
- Never `MakeRaw` the terminal: it clears OPOST and output staircases; `ui.Terminal` keeps ISIG so ctrl+c is SIGINT
- No code comments; the one exception is `internal/jsvm/doc.go`, the embedder contract (keep it current when jsvm behaviour changes)
- Small, focused functions; check errors and return meaningful messages
- External UI tools (picker, pager, notifier, ...) are shell-string config keys with `{placeholders}`, never hard dependencies, documented in readme "External tools"
- Only the user's input and the model's answer print in normal color; tool lines, reasoning and info are dim. The one exception: `/usage` tables, rendered with `ui.Markdown`
- New tests never sleep to wait (a few old ones still do, e.g. `internal/app/idle_test.go`; don't copy them): react to events (channels, job changes, gate or marker files) and shrink time via `runner.second`, `SubagentRunner.Grace`/`Timeout`, `mcp.Manager.CloseGrace`, `llm.Sleep`
- Tools, jsvm and agent tests run with `t.Parallel()`; keep new ones parallel unless they set env or cwd
- `vendor/` is committed and builds download nothing (`GOPROXY=off` works); the one dependency is `golang.org/x/sys`; don't add modules
- config, agent, app and tools tests run under `testenv.Run` (`TestMain`), so they never touch the real config or sessions; use it in new test packages

## Invariant → guard test

- Request bytes per provider (system, tools, messages) → `TestRequestBodiesPinned` (`internal/app/pin_test.go`, goldens `internal/app/testdata`), the only end-to-end check of the byte-stable prefix
- Static prompt directory-free → `TestStaticPromptIsDirectoryFree`, `TestSystemPromptStaticPartIsDirectoryFree`
- Cache marks → `TestSystemSplitIntoCachedParts`, `TestSystemSessionTailIsNotCacheMarked`
- Tool defs unchanged by MCP use → `TestMCPToolDefsByteStable`; compaction request shares the turn's prefix → `TestSummaryRequestMatchesTurnRequest`, `TestSummaryRequestKeepsHistoryUnchanged`
- Kept tail has no reasoning → `TestKeepTail`; tool_use/tool_result pairing → `TestRepair`, `TestCancelDuringToolsKeepsLogValid`
- Config structs ↔ `schema.json` → `TestSchemaMatchesConfig`; `validate.go` ↔ schema enums and minimums → `TestValidateMirrorsSchema`
- Slash commands ↔ `/help` and the `/` picker → `TestCommandTable` (it pins the help column width; the longest usage is `/copy [answer|code|id]`)
- External tool keys ↔ `toolDefaults` → `TestToolKeysMatchToolDefaults` (any other new string key with a default must be excluded there)
- Mode notes as stored in old sessions → `TestStoredModeNotesStillRestore`; mode names and cycle → `TestModeNames`
- Reserved JS names in the run description → `TestRunDescriptionReservedNames`; `-json` output equals the session file → `TestTeeMatchesFile`
- CLI usage text → `TestUsageListsSingleDashFlags`; reasoning stays dim → `TestPrinterReasoningMarkdownStaysDim`
- Suite time → `task test` (5s); data races → `go test -race ./...` (CI)
- No guard test: no code comments, no sleeping tests, readme sync. Find other guards with `grep -rn "^func Test" internal | grep -i <topic>`

## Adding X

- **Config key**: field in `config/config.go` (`Config`, `Provider`, `MCPServer` or `SubagentModel`); `schema.json` (type, default, description); enum or minimum in `validate.go`; default in `applyDefaults`
  - then: `/config` toggle in `app/settings.go` if it is a live boolean; `agent/builtin/qi-config/SKILL.md` if it covers the area; readme "Configure"; run `TestSchemaMatchesConfig`
- **Slash command**: entry in `commandList` (`app/commands.go`: `name`, `args`, `help`, `short`, `run`, `pickArg`); help and picker derive from it; readme "Commands" (hand-kept); tests in `app/commands_test.go`
- **Code-mode function**: constructor returning `*tools.Function` with a `CodeSpec` (`Name`, `Params`, `Options`, `Doc`), `HookNames` (Claude Code names, e.g. `Bash`) and `Path` (per-path locks)
  - mode checks live inside `Run`: `env.readOnly()`, `env.project()`, `env.checkWritable`; `Function.Name` (log, labels) may differ from the JS name `CodeSpec.Name` (`mcp_call` vs `mcp`)
  - tests in `internal/tools` (cell harness in `run_test.go`); `TestRunDescriptionReservedNames` covers the new name
  - register it in `buildAgent` (`app/run.go`); the description is generated by `rundoc.go`, so the goldens change: update them with `-update` and review; readme "Code mode" lists the functions
- **Session entry type**: `session.Type*` constant and writer in `session/session.go`; old files must still load
  - readers to update or check: `session.Replay`/`Load`, `list.go` prefixes, `app/usage.go` (`eachUsage`), `app/render.go`, `agent/subagent.go` (`readEntries`)
- **External UI tool key**: config field, default in `config.toolDefaults`, `schema.json`, `repl.toolKeys` (`app/external.go`); run via `toolAvailable`/`runQuiet`/`runTTY`; readme "External tools"
- **Provider adapter change** (`internal/llm`): unit tests next to the adapter (SSE fixtures), then `TestRequestBodiesPinned`; refresh goldens only for intended changes

## Package map

### internal/app (everything else, exports only `Main`)
- `cli.go` flags on a `flag.FlagSet`, usage, `-version`, `-show`, `-check-config`, exit codes; `version.go` the stamped version, else one from build info
- `run.go` wiring shared by both modes: `resolveConfig`, `newProvider`, `buildAgent` (registers the functions), `loadHooks`, `openSession`, `submitPrompt`
- `headless.go` `-q`/stdin mode; `-json` tees every session entry to stdout via `session.CreateOptions.Tee` (same bytes as the file); `rusage.go` the cpu/peak-mem line it prints
- `repl.go` the interactive loop, mode toggle, status, turns; background job exits start a turn by themselves (`maxAutoTurns` in a row, via `Editor.Wake`/`Woken`)
- `signals.go` signals, resize, ctrl+z job control; `shutdown.go` quit and cleanup; `idle.go` idle compaction; `profile.go` `/profile` and `/effort` switching
- `commands.go` `commandList`, `/help`, dispatch, `/mcp`, `/reload`, image attachment; `copy.go` `/copy`; `pager.go` `/history` in the pager; `notify.go` turn-finished notifications
- `usage.go` `/usage`: sums `usage` of this session, its subagents (meta `parent_session`) and all sessions of 30 days by day, model, project; `prices.go` models.dev estimates (`~`), cached a day
- `settings.go` the `/config` toggles (`run_code`, `show_reasoning`, `terminal_images`), applied live and saved with `config.SetValue` into the active profile when it sets the key
- `picker.go` pickers (`"builtin"` is `ui.Editor.Pick`, else the `picker` command); `browse.go` the builtin `@` browser; `filelist.go` the `@` list for an external picker
- `pickcmd.go` `qi pick` for scripts on `/dev/tty`; `ttyEditor` also serves `qi -r` before the repl exists
- `external.go` the external tool contract: `toolAvailable`, `expandTool` (placeholders become quote-aware `$QI_*` env refs, never shell code), `choose`, `runQuiet`, `runTTY`, `toolKeys`
- `sessions.go` `/resume`, `-r`, `-show`; `render.go` a session as text (plain for `-show`, colored for `/history`; a `run` call shows its code and inner `calls`)

### internal/agent
- `agent.go` `New(Options)`, `Turn`, steering (`Queue`/`DrainQueue`), `Reset`/`Load`, `Reconfigure`, `EditConfig`, `Jobs`, `withRetries`, mode notes
- Locks: `mu` guards `live` (config, provider, session, system prompt, steering, context tokens), never held across I/O; `busy` is held for a whole turn or compaction, guards `conv`
- Lock order is `busy` then `mu`: writers that must not land mid-turn go through `update`. The mode is an atomic `config.Mode`; a `<mode>` note tells the model when it changes
- `Reset` and `Load` drop the `run` VM (`Load` tells the model when the history had `run` calls); compaction keeps it
- `compact.go` summary request on unchanged history, verbatim tail without reasoning between turns, summary only mid-turn, overflow retry, thrash pause, `TryCompactIdle`
- `prompt.go` `LoadPrompt`/`BuildSystemPrompt` (three parts, see Rules; named sections for `/context`), `LoadSkills`, `LoadCommands`, `ProjectRoots`; `context.go` the `/context` breakdown
- `format.go` turn stats, context usage, `FmtTokens`; `images.go` evicts the oldest images past 100 or 24 MB; `scratch.go` the per-session scratch dir
- `builtin.go` + `builtin/qi-config/SKILL.md` the built-in skill and schema, served from memory under `qi://skills/qi-config/` (`tools.Embed`); `frontmatter.go` SKILL.md frontmatter
- `hooks.go` Claude Code hooks: files parsed once (missing skipped, malformed stops startup), one event's hooks run in parallel, `async` ones detached; `Hooks.Attach` wires session, mode, spill
- Hook text the model sees goes through `capText` (cut, rest spilled); `onFailure` and `updatedToolOutput` follow Claude Code (`TestOnFailureBlock`, `TestPostToolUseUpdatedOutput`)
- `trust.go` project hooks need trust: the trust file pins each dir to a fingerprint of its project hooks (`async` included)
- `subagent.go` `SubagentRunner` runs `qi -q … -json` with `SubagentEnv`, `ParentSessionEnv` (meta `parent_session`) and `QI_MCP`; the child writes to `subagents/`, answers with its last text

### internal/tools
- `tools.go` `Function` (one async JS function: `Name`, `Code`, `Run`, `Label`, `Path`, `HookNames`), `Env` (cwd, mode, scratch, jobs), `Result`, hooks types, `Tool.Execute`, `OneLine`, `DoneLine`
- `run.go` `Run(funcs...)` builds the one API tool `run` (`RunName`): a cell in a persistent jsvm VM; `Replace` swaps a function, `Reset` drops the VM; `run` fires no hooks, inner calls do
- `cell.go` the cell loop: parallel calls (capped), per-path locks, timeouts, interrupt; tests shrink time with `runner.second`; `cellout.go` the result text (head and tail, spill file)
- `code.go` `CodeSpec` and JS arguments to tool input; `rundoc.go` the generated `run` description (changing it changes the goldens); `Result.Calls` lands on the tool_result as `calls`
- `fs.go` `read` (images, `crop`, `qi://` files), `write`, `edit`; `files.go` the stale-file guard (write refuses a file changed since last read) and diffs of outside changes for the model
- `bash.go` `bash` with timeout and output cap; `readonly.go` the read-only allowlist (`CheckReadOnly`, `DefaultAllow`; no `$`/backticks, go flag allowlist)
- `project.go` project mode: bash under sandbox-exec (macOS) or bwrap; writable roots are cwd, git dirs, temp, scratch, build caches, ~/.Trash and `project_writable`
- Project mode also: git config/hooks/commondir/gitdir read-only, `.git` pinned; no ptys; network on (macOS unix sockets limited); no usable sandbox refuses bash (`TestProjectMode*`)
- `checkWritable` guards write/edit after resolving symlinks; hooks and MCP run outside the sandbox
- `jobs.go` background bash (`background`, 1h default, 24h max): output to the spill dir, a `<background-job>` notice when it exits, times out or is killed from outside qi (none when qi kills it), all killed on /clear, session load, exit
- A headless answer that ends with jobs running gets one `<background-jobs>` note, then waits for them (tests: `TestBackgroundJob*`, `TestLeftoverProcessesStayKillable`)
- `subagent.go` the `subagent` function (models, efforts, 30 min `SubagentTimeout`); `mcp.go` `tool_search` and `mcp` (`MCPFuncs`), compact listing past 10 tools, never cut by the output cap
- `image.go` scales images to ≤1568px (`imgutil.Resize`); `embedded.go` `Embed`/`Embedded` for `qi://` paths

### internal/jsvm (biggest package; read `doc.go` first)
- `doc.go` the embedder contract: cells, `RunJobs` after every `RunCell`, `Interrupt`/`InterruptAfter`, limits, supported and unsupported language
- `api.go` `New(Options)`, `RunCell`, `RunJobs`, `SetFunc`, promises; `lexer.go`, `parser.go`, `ast.go` parsing; `resolve.go` scopes to frame slots or heap cells
- `compile.go`, `compile_expr.go`, `pattern.go` compile the AST to Go closures; `vm.go`, `value.go`, `object.go`, `props.go` (shapes, inline caches), `ops.go`, `str.go` (ropes, UTF-16 view)
- `async.go` async functions on coroutines (`iter.Pull`); `iterate.go` Map/Set data and iteration; `errors.go` `Exception` and stacks
- `builtins.go` + `b_*.go` the builtins (array, date, error, json, map, number, promise, regexp, string, uri); `date_parse.go`, `casing.go` (case mapping), `regex_bind.go`
- `regex/` the regex engine (backtracking with a step budget; `re2.go` hands eligible patterns to Go's regexp); `regex/testdata/*.json` come from the gitignored `scripts/regex-oracle`

### internal/llm
- `types.go` canonical `Message`/`Block`/`Request`/`Usage`, `Provider`, `SystemParts`, `MergeConsecutive`; `markers.go` in-band text markers (`HookContext`, `AttachedImage`, `IsInBand`)
- `anthropic.go` native API (`ParseClaude` for model features); `openai.go` chat completions incl. OpenRouter; `responses.go` Responses API (`store: false`, encrypted reasoning replayed)
- `sse.go` SSE reader, `APIError`, `ErrStalled`; `stream.go` stream end states (refusal, cut off, empty)
- `retry.go` `Retryable`, `StreamError`; requests retry pre-response failures, `agent.withRetries` mid-stream ones (3 tries, backoff via `llm.Sleep`; cases in `TestRetryableClassification`)
- `llmtest/` the shared scripted fake `Provider` (`Script`, `Steps`, `Fail`, `Requests`) and `Text`/`Call` message helpers

### internal/config
- `config.go` `Config`, `Provider`, `MCPServer`, `SubagentModel`; `Load`, `Resolve`, `Check`, profile overlay, `UseProvider`, `applyDefaults`, `toolDefaults`; `Dir`, `DataDir`, `ShortHome`
- `decode.go` strict, case-sensitive decoding (encoding/json/v2) with file/key errors, embeds `Schema`; `schema.json` the JSON Schema; `validate.go` its enums, minimums, required fields
- `default.go` `SetValue` edits one key in the file (into the profile when it sets the key); `limits.go` `compact_at`/`compact_idle_at` (tokens or %) against `context_window` or a model table
- `mcp.go` per-server tool globs, per-profile `mcp` merge by name (null removes), `QI_MCP` for subagents; `mode.go` `Mode`, `Next` cycles yolo → read-only → project (shift+tab)

### internal/session
- `session.go` append-only JSONL: `Type*` entry constants, `Create`, `Open` (repairs for appending), read-only `Load`/`Entries`, `Replay`, `Repair`, `NewScanner`, `Tee`, `Root`/`Dir`
- `TypeUsage` entries record billed requests that left no message (rejected replies, failed summaries, retries; `TestBilledFailuresRecordTheirUsage`)
- `lock.go` an open session holds an exclusive flock plus a pid file for the error message; `list.go` `List` summarizes a directory's sessions

### internal/ui
- `terminal.go` termios, key decoder, suspend/resume; `termios_bsd.go`/`termios_linux.go` per-OS constants; `crash.go` `OnCrash`, `defer ui.CleanupOnPanic()` in goroutines restores the terminal
- `editor.go` line editor, history, paste, `$EDITOR`, `RunTTY`, steering `Collect`; `layout.go` pure input layout (wrap, cursor, scroll); `pick.go` builtin fuzzy picker (`Pick`, `OnPick`, `OnBack`)
- `printer.go` output and status line; prompt and steering draft are multi-row `Frame`s; tool id `parent.N` is an inner call shown under its parent; `SetRunCode` toggles printing `run` code
- `markdown.go` streaming markdown (TTY only); `table.go` tables; `width.go` display width; `image.go` draws image paths from answers via kitty graphics (`KittyGraphics`: Ghostty/kitty, not tmux)

### internal/mcp
- `mcp.go` stdio MCP client: `Manager` (lazy start, search, call) enforces per-server tool globs, so hidden tools are never listed or called; `tail.go` server stderr tail; `mcptest/` the fake server

### internal/proc
- `proc.go` child processes in their own session (`proc.Session`: bash, hooks, subagents, MCP) or group (`proc.Group`: quiet external tools); TTY tools stay in the foreground group
- Kill policy: SIGTERM the group, SIGKILL after a grace period (`Setup`, `Stop`, `Terminate`); `Settle` waits for pending escalations before exit

### internal/imgutil
- `imgutil.go` area-average `Resize` that reads a row at a time, shared by `tools/image.go` and `ui/image.go`

### internal/strutil
- `strutil.go` `CutStart`, `CutEnd`, `Plural`

### internal/testenv
- `testenv.go` `Run` (shared `TestMain`): clears `QI_*`, `GIT_*`, API keys and `PAGER`, points `QI_CONFIG_DIR`/`QI_SESSION_DIR` at a temp dir

## Helper index (use these instead of writing new ones)

- Strings: `strutil.Plural`, `strutil.CutStart`/`CutEnd`, `tools.OneLine` (one line, capped), `tools.DoneLine`, `ui.Sanitize` (terminal-safe text), `agent.FmtTokens`
- Paths: `config.ShortHome` (`~/` form), `config.ExpandHome`, `config.Dir`/`DataDir`, `session.Root`/`Dir`, `agent.ScratchDir`, `agent.ProjectRoots`
- Config writes: `config.SetValue`; sessions: `session.NewScanner` (large-line scanner), `session.Replay`, `session.Entries`
- Messages: `llm.UserText`, `llm.MergeConsecutive`, `llm.HookContext`, `llm.IsInBand`; images: `imgutil.Resize`, `tools.LoadImage`; markdown: `ui.Markdown`
- Processes: `proc.Setup`, `proc.Stop`, `proc.Settle`; embedded files: `tools.Embed`/`Embedded`
- Tests: `testenv.Run`, `llmtest.Provider` (+ `Script`, `Steps`, `Fail`, `Text`, `Call`), `mcptest` (fake MCP server), `llm.Sleep` (injectable wait)
