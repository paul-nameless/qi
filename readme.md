# aicode

A small coding agent for the terminal. One Go binary, two dependencies, about 20MB of RAM, plain scrolling output (no fullscreen UI).

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

`~/.config/aicode/config.yml` defines providers and profiles:

```yaml
providers:
  openrouter:
    base_url: https://openrouter.ai/api/v1
    api_key_shell: "pass show openrouter-api-key | head -n 1"
  routeway:
    base_url: https://api.routeway.ai/v1
    api_key_shell: "pass show routeway-api-key | head -n 1"
  anthropic:
    api: anthropic                     # native Messages API (base_url optional)
    api_key_shell: "pass show anthropic-api-key | head -n 1"

default_profile: smart

profiles:
  smart:
    provider: openrouter
    model: openai/gpt-6.1-sol
    reasoning_effort: medium
    subagent_models:                   # the model picks one per subagent; /model <name> switches to it
      - name: luna
        model: openai/gpt-6-luna
        use_for: fast and cheap; search, summaries
      - name: terra
        model: openai/gpt-6.1-terra
        use_for: solid mid-tier; focused implementation and reviews
  glm:
    provider: routeway
    model: glm-5.3-flash-uncensored

# shared settings (a profile can override any of them)
subagent_efforts: [low, medium, high]
# cache_ttl: 1h             # Anthropic prompt cache TTL (default 1h interactive, 5m with -q); idle compaction runs 5 min before it expires
# compact_at: 150k          # compact when context passes this: tokens (150000, 150k) or a share of the window (80%)
# compact_idle_at: 100k     # idle compaction threshold, same syntax
# context_window: 1m        # window of this profile's model, for % thresholds (dropped when /model switches); known for Claude, GPT-6 and Gemini, else 200k is assumed
# read_only_allow: [make lint, go test]
# mcp:
#   github:
#     command: github-mcp-server
#     args: [stdio]
#     description: GitHub issues and pull requests
```

`aicode -p glm` picks a profile, `/profile glm` and `/model luna` switch at runtime. A plain `~/.config/aicode/<name>.yml` also works as a profile. The API key can also come from `OPENROUTER_API_KEY`, `OPENAI_API_KEY` or `ANTHROPIC_API_KEY`. Project hooks from `.claude/settings.json` run only after you trust the project once.

## Use

```bash
aicode                          # interactive
aicode -c                       # continue the last session in this directory
aicode -q "fix the failing test"  # headless: progress on stderr, answer on stdout
git diff | aicode -q "review this"
aicode -read-only               # start in read-only mode
```

Compaction summarizes the conversation into a structured handoff (updated on later compactions); between turns it also keeps the newest ~20k tokens verbatim. The summary request resends the history unchanged, so it reads the prompt cache. A "context too long" error triggers one compaction and a retry.

Keys: shift+tab toggles read-only, ctrl+g edits the prompt in `$EDITOR`, alt+enter or ctrl+j inserts a newline, ctrl+c or esc cancels a turn. Text typed while the agent works is queued and delivered after the next tool results.

Commands: `/help`, `/clear`, `/context`, `/reload`, `/compact [focus]`, `/model`, `/profile`, `/effort`, `/skills`, `/exit`, and `/<skill-or-command> [args]`. Typing `/` on an empty line opens a picker (fzf by default; set `picker:` in the config to use another command that reads `name<TAB>description` lines and prints the chosen one).

## External tools

UI features reuse CLI tools you already have. Each is a shell string in the config; `{prompt}`, `{file}`, `{title}`, `{msg}` are replaced shell-quoted and `{picker}` expands to the `picker` value. `{file}` is a temp file aicode writes input to or reads the result from; without it input goes on stdin and the result comes from stdout. A tool is off when set to `off` or when its command is not installed; `/help` shows which are active.

- `notify_cmd` (default `terminal-notifier -title {title} -message {msg}` on macOS, `notify-send {title} {msg}` on Linux): runs when a turn took longer than `notify_after` (default `20s`) and the terminal is not focused (always, if the terminal does not report focus), and when aicode asks whether to trust project hooks.
- `file_picker` (default `(git ls-files -co --exclude-standard 2>/dev/null || fd -t f || find . -type f -not -path '*/.git/*') | {picker}`): typing `@` at the start of a word opens it and inserts the chosen paths (images become `[image N]`). Works with file managers too: `ranger --choosefile={file}`, `yazi --chooser-file={file}`, `nnn -p {file}`.
