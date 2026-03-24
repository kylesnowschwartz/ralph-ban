---
description: Toggle stop_mode between batch and autonomous for this session
allowed-tools: Bash(jq:*), Bash(cat:*), Read
disable-model-invocation: true
---

# Toggle Stop Mode

Toggle `stop_mode` in `.ralph-ban/config.json` between `batch` and `autonomous`.

## Current state

- Config file: `.ralph-ban/config.json`
- Env override: `RALPH_BAN_STOP_MODE` = `!`echo "${RALPH_BAN_STOP_MODE:-<unset>}"``

## Instructions

1. Read the current `stop_mode` from `.ralph-ban/config.json` using `jq -r '.stop_mode // "batch"'`. If the file doesn't exist, treat current mode as `batch`.

2. If `RALPH_BAN_STOP_MODE` is set (shown above as anything other than `<unset>`), warn the user:
   > The `RALPH_BAN_STOP_MODE` env var is set to `<value>`, which overrides the config file. This session was launched with `--auto`. The config change will take effect for hooks only if you unset the env var or restart without `--auto`.

   Ask the user whether to proceed anyway (the change will apply to future sessions launched without `--auto`).

3. Toggle the value:
   - If current is `batch` → write `autonomous`
   - If current is `autonomous` → write `batch`

4. Write the new value using `jq`. Preserve all other fields:
   ```
   jq --arg mode "<new_mode>" '.stop_mode = $mode' .ralph-ban/config.json > .ralph-ban/config.json.tmp && mv .ralph-ban/config.json.tmp .ralph-ban/config.json
   ```
   If the file doesn't exist, create it: `echo '{"stop_mode":"<new_mode>"}' | jq . > .ralph-ban/config.json`

5. Confirm the change: `Toggled stop_mode: <old> → <new>`
