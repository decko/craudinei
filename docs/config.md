# Configuration Reference

Craudinei is configured with a single YAML file. This document covers every configuration field in detail.

## Config file location

Default path: `~/.craudinei/craudinei.yaml`

Override with the `-config` flag:

```bash
./craudinei -config /path/to/craudinei.yaml
```

## File permissions

The config file must have `0600` permissions (owner read/write only). Craudinei validates this at startup and refuses to start if the file is group- or world-readable.

```bash
chmod 0600 ~/.craudinei/craudinei.yaml
```

## Environment variable expansion

Sensitive fields support `${VAR}` syntax for environment variable expansion. For example:

```yaml
telegram:
  token: "${TELEGRAM_BOT_TOKEN}"
  auth_passphrase: "${CRAUDINEI_PASSPHRASE}"
```

Expansion is **targeted** — only fields explicitly marked for expansion receive it. Literal `$` characters in other fields are preserved. This is not `os.ExpandEnv` on the raw YAML; it operates on specific fields after YAML parsing.

Expanded values are never written to logs.

## Hot reload

The `/reload` command reloads the config file at runtime. Only **non-sensitive fields** are reloaded:

| Field | Reloadable |
|-------|------------|
| `telegram.auth_idle_timeout` | Yes |
| `telegram.approval_timeout` | Yes |
| `telegram.progress_interval` | Yes |
| `telegram.inactivity_timeout` | Yes |
| `telegram.allowed_users` | Yes |
| `claude.binary` | Yes |
| `claude.allowed_paths` | Yes |
| `approval.port` | Yes |
| `claude.system_prompt` | Yes |
| `claude.allowed_tools` | Yes |
| `claude.max_turns` | Yes |
| `claude.max_budget_usd` | Yes |
| `claude.default_workdir` | Yes |
| `approval.timeout_bash` | Yes |
| `approval.timeout_edit` | Yes |
| `approval.timeout_write` | Yes |
| `logging.level` | Yes |
| `logging.file` | Yes |
| `logging.audit_file` | Yes |

⚠️ Changes to `allowed_users` take effect immediately on reload. Removing a user ID will revoke their access without restarting the bot.

**Not reloadable** (require restart):
- `telegram.token`
- `telegram.auth_passphrase`

If reload validation fails, the previous config remains active and the error is sent to Telegram.

## Complete reference

### `telegram` section

#### `token` (required)

**Type:** string
**Env expansion:** Yes

Bot token from [@BotFather](https://t.me/BotFather). Supports `${VAR}` expansion.

```yaml
telegram:
  token: "${TELEGRAM_BOT_TOKEN}"
```

#### `allowed_users` (required)

**Type:** list of integers

Telegram user IDs that are allowed to interact with the bot. Messages from unknown user IDs receive a single rate-limited response ("You are not authorized to use this bot.").

To find your Telegram user ID, message [@userinfobot](https://t.me/userinfobot) in Telegram.

```yaml
telegram:
  allowed_users:
    - 123456789
```

#### `auth_passphrase` (required)

**Type:** string
**Env expansion:** Yes

Passphrase for `/auth` authentication. Supports `${VAR}` expansion.

```yaml
telegram:
  auth_passphrase: "${CRAUDINEI_PASSPHRASE}"
```

#### `auth_idle_timeout` (default: `1h`, max: `4h`)

**Type:** duration

How long an authenticated session remains valid before re-authentication is required. Default is 1 hour; maximum is 4 hours.

```yaml
telegram:
  auth_idle_timeout: 1h
```

#### `approval_timeout` (default: `5m`)

**Type:** duration

Default timeout for tool approval requests. Can be overridden per tool type in the `approval` section.

```yaml
telegram:
  approval_timeout: 5m
```

#### `progress_interval` (default: `2m`)

**Type:** duration

How often to send progress updates when a session is running but no events have arrived. After 3 consecutive updates with no new events, the interval doubles.

```yaml
telegram:
  progress_interval: 2m
```

#### `inactivity_timeout` (default: `15m`)

**Type:** duration

How long before the bot warns that a session may be stalled (no events for this period). The user decides whether to continue or stop — there is no auto-kill.

```yaml
telegram:
  inactivity_timeout: 15m
```

---

### `claude` section

#### `binary` (required, must be absolute path)

**Type:** string

Absolute path to the Claude Code binary. Must be an absolute path (not relative) to prevent PATH manipulation attacks.

```yaml
claude:
  binary: "/usr/local/bin/claude"
```

Validation fails at startup if the path is not absolute or the file does not exist.

#### `default_workdir`

**Type:** string

Default working directory for `/begin` when no directory argument is provided. Must be a directory within `allowed_paths`.

```yaml
claude:
  default_workdir: "/home/user/dev"
```

#### `system_prompt` (default: `"Keep each response under 4000 characters. Wrap lines at 80 characters."`)

**Type:** string

Additional system prompt appended to every Claude Code session via `--append-system-prompt`.

```yaml
claude:
  system_prompt: "Keep each response under 4000 characters. Wrap lines at 80 characters."
```

#### `allowed_tools` (default: `["Read", "Grep", "Glob"]`)

**Type:** list of strings

Tools that Claude Code may use without approval. Tools not in this list require approval via the inline button flow.

```yaml
claude:
  allowed_tools:
    - Read
    - Grep
    - Glob
```

#### `max_turns` (default: `50`)

**Type:** integer

Maximum number of Claude Code turns per session. When reached, the session is paused and the user is notified.

```yaml
claude:
  max_turns: 50
```

#### `max_budget_usd` (default: `5.00`)

**Type:** float

Maximum USD budget per session. When reached, the session ends. Note: this is a soft limit; in-flight requests may slightly exceed it. Craudinei tracks cumulative cost from `result` events.

```yaml
claude:
  max_budget_usd: 5.00
```

#### `allowed_paths` (required)

**Type:** list of strings

Allowed working directories. Sessions may only be started in directories within this list. Path validation is defense-in-depth:
1. `filepath.Clean` normalizes `..` components
2. `filepath.EvalSymlinks` resolves symlinks
3. `os.Stat` verifies the resolved path is a directory
4. Prefix matching against each `allowed_path` with a trailing slash (prevents `/home/user/dev` from matching `/home/user/dev-secure`)

```yaml
claude:
  allowed_paths:
    - "/home/user/dev"
    - "/home/user/work"
```

---

### `approval` section

#### `port` (default: `0` = auto-assign)

**Type:** integer

Port for the localhost approval HTTP server. `0` means the kernel assigns an ephemeral port. The server binds to `127.0.0.1` only — never exposed externally.

```yaml
approval:
  port: 0
```

#### `timeout_bash` (default: `5m`)

**Type:** duration

Timeout for `Bash` tool approval requests. Auto-denied if the user does not respond within this time.

```yaml
approval:
  timeout_bash: 5m
```

#### `timeout_edit` (default: `5m`)

**Type:** duration

Timeout for `Edit` tool approval requests.

```yaml
approval:
  timeout_edit: 5m
```

#### `timeout_write` (default: `5m`)

**Type:** duration

Timeout for `Write` tool approval requests.

```yaml
approval:
  timeout_write: 5m
```

---

### `logging` section

#### `level` (default: `"info"`)

**Type:** string

Log level for operational logs. Valid values: `debug`, `info`, `warn`, `error`.

```yaml
logging:
  level: info
```

#### `file` (default: `""` = stdout)

**Type:** string

Path to the operational log file. If empty, logs go to stdout.

```yaml
logging:
  file: "/var/log/craudinei.log"
```

#### `audit_file` (default: `""` = stderr)

**Type:** string

Path to the audit log file. Cannot be disabled. If empty, audit logs go to stderr.

```yaml
logging:
  audit_file: "/var/log/craudinei-audit.log"
```

Audit logs record: all authentication attempts, all commands, all tool decisions (approved/denied/timed out), and session events.

---

## Example configuration

```yaml
telegram:
  token: "${TELEGRAM_BOT_TOKEN}"
  allowed_users:
    - 123456789
  auth_passphrase: "${CRAUDINEI_PASSPHRASE}"
  auth_idle_timeout: 1h
  approval_timeout: 5m
  progress_interval: 2m
  inactivity_timeout: 15m

claude:
  binary: "/usr/local/bin/claude"
  default_workdir: "/home/user/dev"
  system_prompt: "Keep each response under 4000 characters. Wrap lines at 80 characters."
  allowed_tools:
    - Read
    - Grep
    - Glob
  max_turns: 50
  max_budget_usd: 5.00
  allowed_paths:
    - "/home/user/dev"

approval:
  port: 0
  timeout_bash: 5m
  timeout_edit: 5m
  timeout_write: 5m

logging:
  level: info
  file: "/var/log/craudinei.log"
  audit_file: "/var/log/craudinei-audit.log"
```

---

## Security notes

- **Config file permissions** — Always use `0600`. The config contains your bot token and passphrase; it must not be readable by other users on the system.
- **Env vars for secrets** — Store `token` and `auth_passphrase` as `${VAR}` references, not inline. This prevents them from appearing in process argument lists (`ps aux`) or log files.
- **Path validation** — Never put symlinks or parent-directory references (`..`) in `allowed_paths`. The validation layer catches these, but the safest config has clean, absolute paths.
- **Audit log** — The audit log cannot be disabled and is separate from operational logs. Plan for log rotation to prevent disk exhaustion on long-running sessions.
