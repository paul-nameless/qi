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
- Code mode: the model's one tool, `run`, executes JavaScript that calls `read`, `write`, `edit`, `bash`, `jobs` and `subagent`, so a whole step (search, read, edit, test) is one call. See [Code mode](#code-mode). Long builds and servers run as background bash jobs; the agent gets a message when one exits instead of polling, and the line after each answer shows how many are still running (`2 bg jobs`).
- Prompt caching, sessions saved as JSONL, automatic compaction when the context fills up and idle compaction.
- MCP servers start on demand, with per-server limits on which tools the agent may use.
- Two modes, toggled with shift+tab: yolo (default) and read-only (no edits, bash limited to an allowlist).

## Install

```bash
go install github.com/paul-nameless/qi@latest
```

## Configure

`~/.config/qi/config.json` (`$XDG_CONFIG_HOME/qi` when set, or `$QI_CONFIG_DIR`; sessions go to `~/.local/share/qi`, or `$XDG_DATA_HOME/qi`):

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

Top-level keys apply to every profile; a profile overrides them. `qi -p opus` picks a profile for that run; `/profile opus` switches while running and makes it the default for next time. `/model` switches the model for this session only. A provider's key comes from `api_key` (the key itself), `api_key_shell` (a command that prints it, so the key stays out of the file), or the environment: `ANTHROPIC_API_KEY` for `"api": "anthropic"`, `OPENROUTER_API_KEY` only for an openrouter.ai `base_url`, and `OPENAI_API_KEY` for any other host, so an environment key is never sent to a server it doesn't belong to.

A provider's `api` picks the wire protocol: `openai` (chat completions, the default; OpenAI, OpenRouter and compatible servers), `responses` (the OpenAI Responses API, which some models need for tools with reasoning, e.g. `openai.gpt-6.2-sol` on Bedrock at `https://bedrock-mantle.us-east-1.api.aws/openai/v1`) or `anthropic`.

`"system_prompt_file": "prompt.md"` (top level or in a profile; relative to the config directory) replaces the built-in base prompt; instruction files, skills and the environment block are still added. `/profile` switches to the new profile's prompt and `/reload` re-reads the file after you edit it. `qi -system-prompt-file f` does the same for one run, wins over every profile, and is passed to subagents.

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

The agent finds MCP tools with `tool_search()` and calls them with `mcp()`, so they never fill up the tool list. `tools` and `exclude_tools` are glob patterns; hidden tools don't show up in search and calls to them are refused. Top-level servers are available in every profile, and a profile can add servers, replace one, or drop one with `null`. `/mcp` shows what's running. This keeps the model away from the wrong tools; it's not a sandbox, since `bash` can still run anything.

### Code mode

The model gets a single tool, `run`, which executes JavaScript in an in-process VM. The functions `read`, `write`, `edit`, `bash`, `jobs`, `subagent`, `tool_search` and `mcp` are async JavaScript functions there; each call goes through read-only mode, the stale-file check and your PreToolUse/PostToolUse hooks under Claude Code's tool names (`Read`, `Write`, `Edit`, `Bash`, `Agent`/`Task`, `mcp__server__tool`; `jobs` and `tool_search` under their own), and shows as its own line under the `run` line. Variables stay defined between `run` calls; this state lives in memory only and is dropped on `/clear`, on resume and at exit (not on compaction). `run` itself fires no hooks, so a `*` matcher sees each call once.

The terminal shows the whole code of each `run` call (dim, up to 200 lines) with its tool calls under it; `"run_code": false`, globally or in a profile, shows one line per call instead, and `/config` switches it for the session.

## Use

```bash
qi                             # interactive
qi -c                          # continue the last session here
qi -r [id]                     # pick a session to resume, or resume <id>
qi -q "fix the failing test"   # one prompt, answer on stdout
qi -q "list files" -json       # stream the session entries (meta, message, compaction, drop) as JSON lines on stdout; with -c or -r <id>, the meta line then this run's entries
git diff | qi -q "review this"
```

Keys: shift+tab toggles read-only, ctrl+g opens `$EDITOR`, ctrl+r searches past prompts, `@` picks files, alt+enter adds a newline, esc or ctrl+c stops the agent. Text typed while it works is sent with the next tool results.

Images: local PNG, JPEG and GIF files whose paths appear in the final answer of a turn are drawn after it (up to 4) in terminals with the kitty graphics protocol (Ghostty, kitty), not inside tmux or screen. `"terminal_images": false` turns this off, globally or in a profile.

Commands: `/help`, `/keys`, `/status`, `/config`, `/clear`, `/context`, `/compact`, `/reload`, `/resume`, `/history`, `/copy`, `/model`, `/profile`, `/effort`, `/skills`, `/mcp`, `/exit`, plus `/<skill or command>`. `/` on an empty line opens the picker. `/config` shows the config file, the external tools and the settings `run_code` and `show_reasoning`, then opens the picker to switch one for this session (`/config run_code` does it without the picker); the switch is kept across `/profile` and `/model` and never written to the file.

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
