# qi

A small coding agent for the terminal, built just because.

![qi fixing a failing test, then the command menu and /context](demo.gif)

## Philosophy

- **Minimal.** One Go binary, about 20 MB of RAM, plain scrolling output instead of a fullscreen UI.
- **One dependency**, vendored: `golang.org/x/sys` for the terminal. Everything else is the standard library.
- **Reuse.** One small built-in fuzzy picker serves every menu: the `/` command list, `@` files, ctrl+r prompt history, `/resume` sessions and `/config`; `qi pick` offers it to scripts, and fzf can replace it. `$EDITOR` (long prompts), `$PAGER` (history), `pbcopy` (copy) and `terminal-notifier` (notifications) do the rest. See [External tools](#external-tools).
- **Claude Code compatible:** reads `CLAUDE.md`/`AGENTS.md`, skills, commands and hooks from the usual places.

## What it does

- Talks to any OpenAI-compatible API, and Anthropic's native API.
- Code mode only: the model's one tool, `run`, executes JavaScript that calls `read`, `write`, `edit`, `bash`, `jobs`, `subagent`, `tool_search` and `mcp`, so a whole step (search, read, edit, test) is one call, with independent calls in parallel. The JavaScript engine is qi's own. See [Code mode](#code-mode). Long builds and servers run as background bash jobs; the agent gets a message when one exits instead of polling, and the line after each answer shows how many are still running (`2 bg jobs`).
- Prompt caching, sessions saved as JSONL, automatic compaction when the context fills up and idle compaction.
- MCP servers start on demand, with per-server limits on which tools the agent may use.
- Three modes, cycled with shift+tab: yolo (default, no limits), read-only (no edits, bash limited to an allowlist) and project (files can only change inside the working directory, plus temp, package and build caches, `~/.Trash` and `project_writable` dirs, and never git's config, hooks or the `.git` entry itself; bash runs in an OS sandbox (and is refused when none is usable) without terminal devices, so tools that need a pseudo-terminal fail, with the network on and, on macOS, no unix sockets outside the project except DNS and ssh-agent). Project mode is for trying models you don't trust with your files yet: it stops them changing things outside the project, not what they read or send, so secrets on disk and your credentials (git push, gh, cloud CLIs) are still reachable, and your hooks and MCP servers run outside it. It is a guard against a careless model, not a boundary against a hostile one: a model can still leave git settings in a nested repo or another worktree inside the project that run when you use git there. On Linux (bwrap) unix sockets are not limited, and git's `commondir` and `config.worktree` are protected only if they exist when the session starts. Since `.git/config` can't change, git can't record branch upstreams there (`push -u`, `--track` print a warning after doing the rest). Extra writable dirs go in `"project_writable"` (a socket in one of them, e.g. a Docker socket's folder, becomes reachable too). `-project`, `-read-only` or `"mode": "yolo" | "project" | "read-only"` in the config (globally or per profile, e.g. project for a model you're trying out) pick the start mode; `/profile` applies a profile's mode.

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

Top-level keys apply to every profile; a profile overrides them. `qi -p opus` picks a profile for that run; `/profile opus` switches while running and makes it the default for next time; `-model` overrides the model for one run. A provider's key comes from `api_key` (the key itself), `api_key_shell` (a command that prints it, so the key stays out of the file), or the environment: `ANTHROPIC_API_KEY` for `"api": "anthropic"`, `OPENROUTER_API_KEY` only for an openrouter.ai `base_url`, and `OPENAI_API_KEY` for any other host, so an environment key is never sent to a server it doesn't belong to.

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

The model gets a single tool, `run`, which executes JavaScript in an in-process VM. The engine is qi's own (`internal/jsvm`): a subset of JavaScript in plain Go with no dependencies. Classes, generators, getters/setters, `Symbol` and `BigInt` are left out, and each fails with a `SyntaxError` naming it. On qi's benchmarks it is faster than goja and QuickJS in all 16 tests and adds 1.2 MB to the binary. It ran all 501 cells written by real models in testing. The functions `read`, `write`, `edit`, `bash`, `jobs`, `subagent`, `tool_search` and `mcp` are async JavaScript functions there; each call goes through the mode checks, the stale-file check and your PreToolUse/PostToolUse hooks under Claude Code's tool names (`Read`, `Write`, `Edit`, `Bash`, `Agent`/`Task`, `mcp__server__tool`; `jobs` and `tool_search` under their own), and shows as its own line under the `run` line. Hooks follow Claude Code's rules: `"onFailure": "block"` makes a hook that can't start, exits with a code other than 0 or 2, times out or prints invalid JSON block the action (no effect on Stop and async hooks); a PostToolUse hook's `updatedToolOutput` (`updatedMCPToolOutput` for MCP tools), shaped like the `tool_response` it got (`{"output": string, "is_error": bool}`), replaces what the code sees; hook text the model sees over 10,000 characters is cut to its first 2,000 and the rest saved to a file. Variables stay defined between `run` calls; this state lives in memory only and is dropped on `/clear`, on resume and at exit (not on compaction). `run` itself fires no hooks, so a `*` matcher sees each call once.

The terminal shows the whole code of each `run` call (dim, up to 200 lines) with its tool calls under it; `"run_code": false`, globally or in a profile, shows one line per call instead, and `/config` switches it.

## Use

```bash
qi                             # interactive
qi -c                          # continue the last session here
qi -r [id]                     # pick a session to resume, or resume <id>
qi -q "fix the failing test"   # one prompt, answer on stdout
qi -q "list files" -json       # stream the session entries (meta, message, compaction, drop) as JSON lines on stdout; with -c or -r <id>, the meta line then this run's entries
git diff | qi -q "review this"
```

Keys: shift+tab cycles yolo, read-only and project, ctrl+g opens `$EDITOR`, ctrl+r searches past prompts, `@` picks files, alt+c copies the session id (on macOS the terminal must send Option as Alt), alt+enter adds a newline, esc or ctrl+c stops the agent. Text typed while it works is sent with the next tool results.

Images: local PNG, JPEG and GIF files whose paths appear in the final answer of a turn are drawn after it (up to 4) in terminals with the kitty graphics protocol (Ghostty, kitty), not inside tmux or screen. `"terminal_images": false` turns this off, globally or in a profile.

Commands: `/help`, `/keys`, `/status`, `/config`, `/clear`, `/context`, `/compact`, `/usage`, `/reload`, `/resume`, `/history`, `/copy`, `/profile`, `/effort`, `/skills`, `/mcp`, `/exit`, plus `/<skill or command>`. `/` on an empty line opens the picker. `/config` shows the config file, the external tools and the settings `run_code`, `show_reasoning` and `terminal_images`, then opens the picker: Enter switches the highlighted setting and saves it to the config file (into the active profile when the profile sets it), Esc closes. `/config run_code` does it without the picker. `/copy` copies the last answer, `/copy code` its last code block and `/copy id` (or alt+c) the session id. `/usage` shows spend for this session (with its subagents), each of the last 7 days, the last 30 days, and the 30 days by model and by project, from all session files, as styled tables. Cost the provider reports (OpenRouter) is exact; the rest is estimated, marked `~`, from [models.dev](https://models.dev) prices, which qi downloads when `/usage` runs, at most once a day, and caches in its data dir (Ctrl-C stops it). Prices are matched by model id: `vendor/model` ids at OpenRouter's price, other ids at Bedrock's, Anthropic's, OpenAI's or Google's own price, so the same id served by another provider gets that list price; Anthropic cache writes in interactive sessions are priced at the 1-hour rate qi uses there. Models models.dev does not list that way (for example Routeway's) show "no price". It counts what the session files recorded: rejected replies, failed summaries and retried attempts are included when the provider reported their usage before failing.

## External tools

Whenever qi needs you to pick something, it opens its built-in picker: type to filter (fuzzy; a capital letter makes it case-sensitive), up/down to move, Enter to pick, Esc to close (in the `/` menu, Backspace on an empty filter closes it and removes the `/`; in its argument lists it removes the space after the command). In the `/` menu and its argument lists (`/profile`, `/copy`, ...), Tab puts the highlighted item into the input line without running it (`/` then `comp` Tab gives `/compact ` for adding a focus); elsewhere Tab picks like Enter.

| You press | You get |
|---|---|
| `/` on an empty line | command menu: commands and skills with descriptions |
| `@` | file browser: every entry of a folder, folders first; Enter opens a folder, left (or backspace on an empty filter) goes up, tab picks the folder itself |
| ctrl+r | prompt history, newest first |
| `/resume`, `qi -r` | sessions, searchable by their first message |

`qi pick` is the same picker for scripts: it reads lines on stdin (a tab splits a name from a description, `-d` searches descriptions too), prints the chosen line and exits 1 when cancelled; `qi pick -f` browses files instead, so `vim "$(qi pick -f)"` works.

Other jobs go to the usual tools the same way: `$PAGER` (or `less -R`) for `/history`, `pbcopy`/`wl-copy`/`xclip` for `/copy`, and `terminal-notifier`/`notify-send` to tell you a turn finished while you were in another window.

Each tool is a shell command in the config (`picker`, `file_picker`, `history_picker`, `session_picker`, `pager`, `copy_cmd`, `notify_cmd`), so you can swap in anything that reads lines and prints a choice, or a file manager for files. `"builtin"` is the default picker; with fzf, `/resume` shows a preview of each session but `/config` closes after one switch:

```json
{
  "picker": "fzf --height=40% --reverse --no-multi --bind=ctrl-z:ignore --delimiter='\t' --nth=1 --prompt={prompt}",
  "file_picker": "yazi --chooser-file={file}",
  "notify_cmd": "off"
}
```

`"off"` disables one, and a tool that isn't installed is skipped. `/config` shows which are active. Values like `{file}` and `{msg}` are passed as environment variables, never pasted into the command; [`schema.json`](internal/config/schema.json) has the details and defaults.
