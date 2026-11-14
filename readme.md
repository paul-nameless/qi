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
# cache_ttl: 1h             # prompt cache TTL; idle compaction runs 5 min before it expires
# compact_at: 150000        # compact when context passes this many tokens
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

Keys: shift+tab toggles read-only, ctrl+g edits the prompt in `$EDITOR`, alt+enter or ctrl+j inserts a newline, ctrl+c or esc cancels a turn. Text typed while the agent works is queued and delivered after the next tool results.

Commands: `/help`, `/clear`, `/compact`, `/model`, `/profile`, `/effort`, `/skills`, `/exit`, and `/<skill-or-command> [args]`. Typing `/` on an empty line opens a picker (fzf by default; set `picker:` in the config to use another command that reads `name<TAB>description` lines and prints the chosen one).
