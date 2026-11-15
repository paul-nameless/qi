# qi

A small coding agent for the terminal, built just because.

![qi fixing a failing test, then the fzf command menu and /context](demo.gif)

## Philosophy

- **Minimal.** One Go binary, about 20 MB of RAM, plain scrolling output instead of a fullscreen UI.
- **One dependency**, vendored: `golang.org/x/sys` for the terminal. Everything else is the standard library.
- **Reuse.** Every menu is fzf: the `/` command list, `@` file picker, ctrl+r prompt history and `/resume` sessions. qi just pipes lines into fzf and reads back the choice, so there's no picker, fuzzy matcher or preview pane to write and maintain. The same goes for `$EDITOR` (long prompts), `$PAGER` (history), `pbcopy` (copy) and `terminal-notifier` (notifications). See [External tools](#external-tools).
- **Claude Code compatible:** reads `CLAUDE.md`/`AGENTS.md`, skills, commands and hooks from the usual places.

## What it does

- Talks to any OpenAI-compatible API, and Anthropic's native API.
- Tools: `read`, `write`, `edit`, `bash`, `subagent`. Several calls in one response run in parallel.
- Prompt caching, sessions saved as JSONL, automatic compaction when the context fills up and idle compaction.
- MCP servers start on demand, with per-server limits on which tools the agent may use.
- Two modes, toggled with shift+tab: yolo (default) and read-only (no edits, bash limited to an allowlist).

## Install

```bash
go install github.com/paul-nameless/qi@latest
```

## Configure

`~/.config/qi/config.json`:

```json
{
  "providers": {
    "openrouter": {
      "base_url": "https://openrouter.ai/api/v1",
      "api_key_shell": "pass show openrouter-api-key"
    },
    "anthropic": {
      "api": "anthropic",
      "api_key": "sk-i-like-to-keep-api-keys-in-plain-config-files"
    }
  },
  "default_profile": "luna",
  "profiles": {
    "luna": {"provider": "openrouter", "model": "openai/gpt-6-luna", "reasoning_effort": "low"},
    "opus": {
      "provider": "anthropic",
      "model": "claude-opus-5-5",
      "subagent_models": [
        {"name": "haiku", "model": "claude-haiku-5-5", "use_for": "fast and cheap; search, summaries"}
      ]
    }
  }
}
```

Top-level keys apply to every profile; a profile overrides them. `qi -p opus` picks a profile, `/profile` and `/model` switch while running. A provider's key comes from `api_key` (the key itself), `api_key_shell` (a command that prints it, so the key stays out of the file), or the environment: `OPENROUTER_API_KEY`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`.

Every key is described in [`internal/config/schema.json`](internal/config/schema.json). `qi -check-config` checks the config without calling any API. Or just ask qi to change a setting: the built-in `qi-config` skill knows the format.

### MCP

```json
{
  "mcp": {
    "github": {
      "command": "github-mcp-server",
      "args": ["stdio"],
      "tools": ["get_*", "list_*", "create_issue"],
      "exclude_tools": ["*_secret*"]
    }
  },
  "profiles": {
    "work": {"mcp": {"jira": {"command": "jira-mcp"}}},
    "offline": {"mcp": {"github": null}}
  }
}
```

The agent finds MCP tools with `tool_search` and calls them with `mcp_call`, so they never fill up the tool list. `tools` and `exclude_tools` are glob patterns; hidden tools don't show up in search and calls to them are refused. Top-level servers are available in every profile, and a profile can add servers, replace one, or drop one with `null`. `/mcp` shows what's running. This keeps the model away from the wrong tools; it's not a sandbox, since `bash` can still run anything.

## Use

```bash
qi                             # interactive
qi -c                          # continue the last session here
qi -r                          # pick a session to resume
qi -q "fix the failing test"   # one prompt, answer on stdout
git diff | qi -q "review this"
```

Keys: shift+tab toggles read-only, ctrl+g opens `$EDITOR`, ctrl+r searches past prompts, `@` picks files, alt+enter adds a newline, esc or ctrl+c stops the agent. Text typed while it works is sent with the next tool results.

Commands: `/help`, `/keys`, `/config`, `/clear`, `/context`, `/compact`, `/reload`, `/resume`, `/history`, `/copy`, `/model`, `/profile`, `/effort`, `/skills`, `/mcp`, `/exit`, plus `/<skill or command>`. `/` on an empty line opens the picker.

## External tools

qi has no built-in menus or viewers. Whenever it needs you to pick something, it runs fzf with a list of lines on stdin and takes the line you choose. That one tool covers four features:

| You press | You get | fzf reads |
|---|---|---|
| `/` on an empty line | command menu | commands and skills with descriptions |
| `@` | file picker | this directory's files, git files first |
| ctrl+r | prompt history | your past prompts, newest first |
| `/resume`, `qi -r` | session picker | sessions with a preview of each |

Other jobs go to the usual tools the same way: `$PAGER` (or `less -R`) for `/history`, `pbcopy`/`wl-copy`/`xclip` for `/copy`, and `terminal-notifier`/`notify-send` to tell you a turn finished while you were in another window.

Each tool is a shell command in the config (`picker`, `file_picker`, `history_picker`, `session_picker`, `pager`, `copy_cmd`, `notify_cmd`), so you can swap in anything that reads lines and prints a choice, or a file manager for files:

```json
{
  "picker": "fzf --height=40% --reverse",
  "file_picker": "yazi --chooser-file={file}",
  "notify_cmd": "off"
}
```

`"off"` disables one, and a tool that isn't installed is skipped. `/config` shows which are active. Values like `{file}` and `{msg}` are passed as environment variables, never pasted into the command; [`schema.json`](internal/config/schema.json) has the details and defaults.
