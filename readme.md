# aicode

A small coding agent for the terminal. One Go binary, one dependency, about 20MB of RAM, plain scrolling output (no fullscreen UI).

- Works with OpenRouter, any OpenAI-compatible API, and Anthropic.
- Tools: `read`, `write`, `edit`, `bash`, `subagent`. Several calls in one response run in parallel.
- Prompt caching always on, sessions saved as JSONL, `-c` to continue.
- Claude Code compatible: reads `CLAUDE.md`/`AGENTS.md`, skills from `~/.claude/skills` and `.claude/skills`, commands from `.claude/commands`, hooks from `.claude/settings.json`.
- Two modes, toggled with shift+tab: yolo (default) and read-only (no write/edit, bash limited to an allowlist).

## Install

```bash
go install github.com/paul-nameless/aicode@latest
```

## Configure

`~/.config/aicode/config.json` defines providers and profiles:

```json
{
  "providers": {
    "openrouter": {
      "base_url": "https://openrouter.ai/api/v1",
      "api_key_shell": "pass show openrouter-api-key | head -n 1"
    },
    "routeway": {
      "base_url": "https://api.routeway.ai/v1",
      "api_key_shell": "pass show routeway-api-key | head -n 1"
    },
    "anthropic": {
      "api": "anthropic",
      "api_key_shell": "pass show anthropic-api-key | head -n 1"
    }
  },
  "default_profile": "smart",
  "profiles": {
    "smart": {
      "provider": "openrouter",
      "model": "openai/gpt-6.1-sol",
      "reasoning_effort": "medium",
      "subagent_models": [
        {"name": "luna", "model": "openai/gpt-6-luna", "use_for": "fast and cheap; search, summaries"},
        {"name": "terra", "model": "openai/gpt-6.1-terra", "use_for": "solid mid-tier; focused implementation and reviews"}
      ]
    },
    "glm": {"provider": "routeway", "model": "glm-5.3-flash-uncensored"}
  },
  "subagent_efforts": ["low", "medium", "high"]
}
```

Top-level keys are shared settings; a profile can set any of them except `profiles` and `default_profile`, and replaces that key's whole value (`null` resets it to the default). `api: anthropic` uses the native Messages API. The model picks one of `subagent_models` per subagent, and `/model <name>` switches to it. Other keys:

- `cache_ttl`: Anthropic prompt cache TTL (default `1h` interactive, `5m` with `-q`); idle compaction runs 5 minutes before it expires.
- `compact_at`: compact when context passes this, in tokens (`150000`, `"150k"`) or a share of the window (`"80%"`). `compact_idle_at` is the idle threshold, same syntax.
- `context_window`: window of the profile's model, for % thresholds (dropped when `/model` switches); known for Claude, GPT-6 and Gemini, else 200k is assumed.
- `read_only_allow`: extra commands allowed in read-only mode, e.g. `["make lint", "go test"]`.
- `mcp`: stdio MCP servers, e.g. `{"github": {"command": "github-mcp-server", "args": ["stdio"], "description": "GitHub issues and pull requests"}}`.
- `builtin_skills`: `false` hides the built-in `aicode-config` skill, which lets the agent read and change these settings.

Every key is described in the JSON Schema [`internal/config/schema.json`](internal/config/schema.json); a `"$schema"` key pointing at it is allowed. Unknown keys and values outside the schema are errors. `aicode -check-config` loads the config and every profile (inline and `~/.config/aicode/*.json`) without calling any API. After editing, `/profile <name>` re-reads the file; prompt keys (`skill_dirs`, `instruction_files`, `builtin_skills`) also need `/reload`, and `mcp` and `hook_files` need a restart.

`aicode -p glm` picks a profile, `/profile glm` and `/model luna` switch at runtime. A plain `~/.config/aicode/<name>.json` also works as a profile. The API key can also come from `OPENROUTER_API_KEY`, `OPENAI_API_KEY` or `ANTHROPIC_API_KEY`. Project hooks from `.claude/settings.json` run only after you trust the project once.

Older versions read YAML (`config.yml`). Convert it once with the same keys, e.g. `yq -o json config.yml > config.json`; aicode stops with a hint while only the YAML file exists.

## Use

```bash
aicode                          # interactive
aicode -c                       # continue the last session in this directory
aicode -r                       # pick a session of this directory to resume
aicode -q "fix the failing test"  # headless: progress on stderr, answer on stdout
git diff | aicode -q "review this"
aicode -read-only               # start in read-only mode
```

Compaction summarizes the conversation into a structured handoff (updated on later compactions); between turns it also keeps the newest ~20k tokens verbatim. The summary request resends the history unchanged, so it reads the prompt cache. A "context too long" error triggers one compaction and a retry.

Keys: shift+tab toggles read-only, ctrl+g edits the prompt in `$EDITOR`, ctrl+r searches prompt history, `@` picks files, alt+enter or ctrl+j inserts a newline, ctrl+c or esc cancels a turn. Text typed while the agent works is queued and delivered after the next tool results.

Commands: `/help`, `/keys`, `/config`, `/clear`, `/context`, `/reload`, `/compact [focus]`, `/resume`, `/history`, `/copy`, `/model`, `/profile`, `/effort`, `/skills`, `/exit`, and `/<skill-or-command> [args]`. Typing `/` on an empty line opens a picker (fzf by default; set `picker:` in the config to use another command that reads `name<TAB>description` lines and prints the chosen one).

## External tools

UI features reuse CLI tools you already have. Each is a shell string in the config run with `sh -c`. `{prompt}`, `{file}`, `{title}`, `{msg}` become references to environment variables (`$AICODE_MSG`, ...), so their values are never parsed as shell code: bare or in double quotes they give the value; inside single quotes they stay `"$AICODE_MSG"` for a nested shell (`sh -c '...'`, fzf `--preview '...'`) to expand. A nested script in double quotes, or a non-shell interpreter like `osascript`, should read the variables itself (`system attribute "AICODE_MSG"`). `{picker}` expands to the `picker` value. `{file}` is a temp file aicode writes input to or reads the result from; without it input goes on stdin and the result comes from stdout. A tool is off when set to `off` or when its command is not installed; `/config` shows which are active.

- `notify_cmd` (default `terminal-notifier -title {title} -message {msg}` on macOS, `notify-send {title} {msg}` on Linux): runs when a turn ends while the terminal is not focused. Needs a terminal that reports focus (iTerm2, Ghostty, kitty, WezTerm, agterm; in tmux `set -g focus-events on`).
- `file_picker` (default `{picker}`): typing `@` at the start of a word opens it with this directory's files on stdin and inserts the chosen paths. The list has git files first (tracked and untracked, shallowest first), then all other files including ignored ones, breadth-first so top-level files come before deep ones; `.git` is skipped and the list stops at 20000 files or 2 seconds of walking, so big trees like `node_modules` can't stall it (images become `[image N]`). Works with file managers too: `ranger --choosefile={file}`, `yazi --chooser-file={file}`, `nnn -p {file}`.
- `history_picker` (default `{picker}`): ctrl+r picks a prompt from history (newest first) and puts it in the input line without sending it.
- `session_picker` (default `{picker} --with-nth=2.. --nth=.. --preview '"$AICODE_BIN" -show {1}' --preview-window=right,60%`): `/resume` and `aicode -r` pick a session of this directory (newest first, with age, message count and first prompt) and switch to it. `$AICODE_BIN` is set to the running aicode binary for every tool; `aicode -show <id>` prints a session as plain text. The default uses fzf flags, so set it to `{picker}` with another picker.
- `pager` (default `$PAGER`, else `less -R`): `/history` opens the whole conversation in color (answers styled like the live output, tool calls dim) with their full results (as the model saw them; a long result shows the path of its full output). `LESS=-R` is set for tools when `LESS` is unset, so a plain `less` pager shows the colors.
- `copy_cmd` (default `pbcopy` on macOS, `wl-copy` or `xclip -selection clipboard` on Linux): `/copy` copies the last answer, `/copy code` its last code block.
