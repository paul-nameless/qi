---
name: qi-config
description: Read and change qi's own settings. Use when the user asks about qi's config: profiles, providers, models, effort, compaction, MCP servers, hooks, skills or the picker.
---

# qi config

qi is the agent you are running in. Its settings are JSON files.

## Files

- `~/.config/qi/config.json`: the main config. The directory is `$QI_CONFIG_DIR` when set, else `$XDG_CONFIG_HOME/qi` when `XDG_CONFIG_HOME` is set; `~/.config/qi` below means that directory. `qi -config path.json` uses another file. Sessions live in `$QI_SESSION_DIR`, else `$XDG_DATA_HOME/qi/sessions`, else `~/.local/share/qi/sessions`.
- Profiles live under `profiles` in the config (selected with `-p name`, `default_profile` or `/profile name`).
- `system_prompt_file` (top level or in a profile; relative to the config directory) replaces the built-in base prompt; instruction files, skills and the environment block are still added.
- Related files that are not JSON config: hooks in `~/.config/qi/settings.json` (Claude Code settings format), skills in `~/.config/qi/skills/<name>/SKILL.md`, commands in `~/.config/qi/commands/*.md`.
- `schema.json` next to this file is the JSON Schema (draft 2020-12) for every key: types, allowed values, defaults and what each key does. Read it before adding a key you are not sure about.

## Format

- Plain JSON: no comments, no trailing commas. Pretty-print with 2-space indentation and end the file with a newline.
- Change only what the user asked for with the edit tool, keeping the existing key order. Write a whole file only when creating it.
- Unknown keys are errors, so use the exact names from the schema. An optional `"$schema"` key is allowed and ignored.
- Token counts are numbers or strings: `150000`, `"150k"`, `"1.5m"`. `compact_at` and `compact_idle_at` also take a share of the window: `"80%"`.

## How settings combine

1. The top-level keys are the shared settings.
2. The profile is `-p name`, else `default_profile`. Every key a profile sets replaces the top-level value as a whole, including objects (`providers`) and arrays (`subagent_models`); `mcp` is the exception (see MCP servers). `null` resets a key to its default. A profile cannot contain `profiles` or `default_profile`.
3. `provider` names an entry in `providers` (or the built-ins `claude`, `anthropic`, `openai`) and sets `api`, `base_url`, `api_key`, `api_key_shell` and `cache_control` from it. Connection keys set in the profile itself win over the provider's.
4. The flags `-provider`, `-model`, `-effort` and `-system-prompt-file` override everything for one run; `-system-prompt-file` also holds across `/profile` and reaches subagents. `/model` and `/effort` switch at runtime without saving anything. `/profile <name>` switches and also saves it as `default_profile` in the config file (only that value changes; formatting is kept); `-p` never saves.

## Check your change

Run `qi -check-config` (add `-config path.json` when relevant). It loads the config and every profile without calling any API, checks allowed values, and prints the file and key of the first problem.

When a change takes effect in a running session:
- Most keys, including `system_prompt_file`, `skill_dirs`, `instruction_files` and `builtin_skills`: after `/profile <name>`, which re-reads the file and rebuilds the system prompt.
- Edits inside the system prompt file, skills, commands or CLAUDE.md files: `/reload`.
- `mcp`, `hook_files`, `read_only`: only after restarting qi.

## MCP servers

- Top-level `mcp` servers are global: every profile gets them. A profile's `mcp` is merged by server name: a new name adds a server for that profile, an existing name replaces that server's whole entry (repeat `command` and everything else), `"name": null` removes a global server, `"mcp": null` removes all.
- `tools` (allow list) and `exclude_tools` (deny list) are glob patterns on the tool name: `*`, `?`, `[a-z]`, `[^a-z]`; case-sensitive; `*` crosses `/`. A tool is enabled when `tools` is unset or matches, and `exclude_tools` doesn't. Omit `tools` instead of writing `[]`. Change them only when the user asks: they are the user's decision.
- After a restart, `/mcp` shows each server's status, enabled tools and hidden count. `/profile` doesn't change MCP servers, and subagents keep their parent's setup.
- Filters are a guard rail, not a sandbox: `bash` can still run anything.

## Secrets

- Treat `api_key` values and every value under `mcp.<name>.env` as secrets: never print, copy or move them, and never put one in a file that did not have it. Prefer `api_key_shell` (for example `"pass show openrouter-api-key | head -n 1"`) over `api_key`.
- Before reading a config file, check for secrets with `grep -cE '"api_key"|"env"' FILE`. If there are any, don't read the whole file: look at only the lines you need (for example `grep -n '"model"' FILE` or a read with offset and limit) and keep `api_key` lines and the contents of `env` objects out of your output and edit strings.

## Turning this skill off

Set `"builtin_skills": false` in the config.
