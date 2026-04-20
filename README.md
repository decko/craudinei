# Craudinei

**Craudinei** is a Go binary that bridges Claude Code CLI sessions with Telegram. It lets you control a Claude Code coding assistant from your phone — send prompts, stream responses, approve destructive tool calls, and monitor long-running sessions.

[![Go version](https://img.shields.io/badge/Go-1.22%2B-blue)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache%202.0-green)](LICENSE)

## What you can do

- **Remote coding assistant** — Send prompts from Telegram to a Claude Code session running on your machine or server. See responses streamed back in real time.
- **Approve tool calls** — Destructive operations like `Bash`, `Edit`, and `Write` require your approval via inline buttons before Claude Code proceeds.
- **Monitor sessions** — Get notified when sessions complete, error out, or stall. Periodic progress updates keep you informed during long tasks.
- **Session management** — Start, stop, cancel, and resume sessions. Crash recovery restores your context.

## Security disclaimer

> **Bot chats with Telegram are not end-to-end encrypted.** All prompts, code, file contents, and tool outputs flow through Telegram's servers in cleartext. Do not use Craudinei with proprietary, classified, or sensitive source code.

## Features

| Feature | Description |
|---------|-------------|
| 9 bot commands | `/begin`, `/stop`, `/cancel`, `/status`, `/auth`, `/help`, `/resume`, `/sessions`, `/reload` |
| State machine guards | Commands are validated against the current session state |
| Two-layer auth | Telegram user ID whitelist + passphrase authentication |
| Rate limiting | 3 failures in 5 minutes triggers lockout with exponential backoff |
| Edit-based streaming | Responses update in-place (~1 edit/sec); outputs >16K chars sent as file attachments |
| Rich UI screens | Welcome, home, directory picker, sessions list, status bar, approval cards |
| Priority messaging | High-priority messages (results, errors) are preserved when possible; low-priority (progress, status) dropped when queue is full |
| Approval flow | Inline approve/deny/snooze buttons with timeout handling |
| Input sanitization | Control char stripping, 4096-char cap, JSON injection prevention |
| Path validation | Defense-in-depth: `Clean`, `EvalSymlinks`, existence check, prefix match |
| Graceful shutdown | SIGINT → 5s wait → SIGKILL, with Telegram notification before shutdown |
| Session persistence | Crash recovery via `~/.craudinei/session.json` |
| Hot config reload | Non-sensitive fields reloadable via `/reload` |
| Audit logging | Dedicated audit log for auth attempts, commands, tool decisions, and session events |

## Prerequisites

- Go 1.22 or later
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and in your `PATH`
- A Telegram bot token from [@BotFather](https://t.me/BotFather)
- Your Telegram user ID (use [@userinfobot](https://t.me/userinfobot) to find it)
- An `ANTHROPIC_API_KEY` environment variable set with your Anthropic API key

## Quick start

### 1. Create a Telegram bot

Open [@BotFather](https://t.me/BotFather) in Telegram and create a new bot:

```
/newbot
```

Follow the prompts to choose a display name and username. Copy the bot token it gives you.

For full setup instructions (bot description, profile picture, security), see [docs/botfather-setup.md](docs/botfather-setup.md).

### 2. Build the binary

```bash
git clone https://github.com/decko/craudinei
cd craudinei
make build
```

### 3. Configure

Create your config file at `~/.craudinei/craudinei.yaml`:

```bash
mkdir -p ~/.craudinei
chmod 700 ~/.craudinei
cp craudinei.yaml.example ~/.craudinei/craudinei.yaml
$EDITOR ~/.craudinei/craudinei.yaml
```

At minimum, set:
- `telegram.token` — your bot token from BotFather
- `telegram.allowed_users` — your Telegram user ID
- `telegram.auth_passphrase` — a passphrase for `/auth`
- `claude.binary` — absolute path to the Claude Code binary
- `claude.allowed_paths` — directories where sessions may run

The config file must have `0600` permissions (owner read/write only). Craudinei will refuse to start otherwise.

See [docs/config.md](docs/config.md) for the full configuration reference.

### 4. Set environment variables

```bash
export ANTHROPIC_API_KEY=sk-ant-...
export TELEGRAM_BOT_TOKEN=123456789:ABCDEF...
export CRAUDINEI_PASSPHRASE=your-secret-passphrase
```

Or put them in a file referenced by your systemd service or launch script.

### 5. Run

```bash
./craudinei -config ~/.craudinei/craudinei.yaml
```

### 6. Authenticate and start a session

Open your bot in Telegram. You'll see a welcome screen. Click **Authenticate** and send your passphrase, or type `/auth <your-passphrase>`.

Once authenticated, use `/begin <directory>` to start a session, or pick a project from the home screen.

## Bot commands

| Command | Description | State guard |
|---------|-------------|-------------|
| `/begin [dir]` | Start a new Claude Code session in `dir` (or `default_workdir`) | Idle, Crashed |
| `/stop` | Stop the current session | Running, WaitingApproval |
| `/cancel` | Interrupt the current task (sends SIGINT) | Running, WaitingApproval |
| `/status` | Show session state, directory, uptime, cost, token usage | Any state |
| `/auth <passphrase>` | Authenticate for this Telegram session | Any state (rate-limited) |
| `/help` | Show all commands with descriptions | Any state |
| `/resume [id]` | Resume a previous session (most recent if no `id`) | Idle, Crashed |
| `/sessions` | List recent sessions with timestamps | Idle, Crashed |
| `/reload` | Reload config (non-sensitive fields only) | Any state |

## Configuration

Craudinei is configured via a single YAML file. The default location is `~/.craudinei/craudinei.yaml`, overridable with `-config`.

Configuration supports targeted environment variable expansion for sensitive fields using `${VAR}` syntax. Literal `$` characters in non-secret fields are preserved.

For the complete reference, see [docs/config.md](docs/config.md).

## Deployment

Craudinei can run as a systemd service, in a container, or as a background process.

For detailed deployment instructions including systemd unit files, Docker setup, environment variable management, log rotation, and security hardening, see [docs/deployment.md](docs/deployment.md).

## Development

```bash
make build          # Build the binary to ./craudinei
make test           # Run tests with -race enabled
make lint           # Run gofmt and go vet
make check          # lint + test + build-all
make install-hooks email=brito.afa@gmail.com  # Install git hooks
```

## Contributing

Contributions are welcome. Please follow the commit conventions described in [AGENTS.md](AGENTS.md):
- Conventional Commits format for commit titles
- `Assisted-by` trailer with model name for AI-assisted commits
- Atomic commits (one logical change per commit)

## License

Copyright 2025. Licensed under the Apache License 2.0. See [LICENSE](LICENSE) for details.
