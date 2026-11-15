---
name: aicode-config
description: Read and change aicode's own settings. Use when the user asks to change or inspect aicode's config, profiles, providers, models, subagent models, reasoning effort, compaction thresholds, context window, read-only allowlist, MCP servers, hooks, skill or instruction directories, or the picker.
---

# aicode config

aicode is the agent you are running in. Its settings are JSON files.

## Files

- `~/.config/aicode/config.json`: the main config (the directory is `$AICODE_CONFIG_DIR` when set). `aicode -config path.json` uses another file.
- Profiles live under `profiles` in the config, or in their own file `~/.config/aicode/<name>.json` (selected with `-p name`; `-p` also takes a path).
- Related files that are not JSON config: hooks in `~/.config/aicode/settings.json` (Claude Code settings format), skills in `~/.config/aicode/skills/<name>/SKILL.md`, commands in `~/.config/aicode/commands/*.md`.
- `schema.json` next to this file is the JSON Schema (draft 2020-12) for every key: types, allowed values, defaults and what each key does. Read it before adding a key you are not sure about.

## Format

- Plain JSON: no comments, no trailing commas. Pretty-print with 2-space indentation and end the file with a newline.
- Change only what the user asked for with the edit tool, keeping the existing key order. Write a whole file only when creating it.
- Unknown keys are errors, so use the exact names from the schema. An optional `"$schema"` key is allowed and ignored.
- Token counts are numbers or strings: `150000`, `"150k"`, `"1.5m"`. `compact_at` and `compact_idle_at` also take a share of the window: `"80%"`.

## How settings combine

1. The top-level keys are the shared settings.
2. The profile is `-p name`, else `default_profile`. Every key a profile sets replaces the top-level value as a whole, including objects (`providers`, `mcp`) and arrays (`subagent_models`).
3. `provider` names an entry in `providers` (or the built-ins `claude`, `anthropic`, `openai`) and sets `api`, `base_url`, `api_key`, `api_key_shell` and `cache_control` from it. Connection keys set in the profile itself win over the provider's.
4. The flags `-provider`, `-model` and `-effort` override everything for one run. `/profile`, `/model` and `/effort` switch at runtime without saving anything.

## Check your change

Run `aicode -check-config` (add `-config path.json` or `-p name` when relevant). It loads the config and every profile without calling any API and prints the file and key of the first problem. Changes take effect on the next start or the next `/profile` switch.

## Secrets

- Never print, copy or move API keys, and never put one in a file that did not have it. Prefer `api_key_shell` (for example `"pass show openrouter-api-key | head -n 1"`) over `api_key`.
- Before reading a config file, check for literal keys with `grep -c '"api_key"' FILE`. If there are any, view it redacted, for example `sed -E 's/("api_key": *")[^"]*/\1REDACTED/' FILE`, and keep key values out of edit strings.

## Turning this skill off

Set `"builtin_skills": false` in the config.
