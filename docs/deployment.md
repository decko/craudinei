# Deployment Guide

This guide covers production deployment of Craudinei: building, running as a systemd service, containerization, environment setup, log management, monitoring, and security hardening.

## Prerequisites

- Go 1.22 or later (to build from source)
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed on the host
- A Telegram bot token from [@BotFather](https://t.me/BotFather)
- Your Telegram user ID
- `ANTHROPIC_API_KEY` environment variable set with your Anthropic API key

## Building from source

```bash
git clone https://github.com/decko/craudinei
cd craudinei
make build
```

The binary is created at `./craudinei` in the repository root.

For production, you may want to build with optimizations and no debug info:

```bash
CGO_ENABLED=0 go build -ldflags="-s -w" -o /usr/local/bin/craudinei ./cmd/craudinei
```

## Running as a systemd service

Create a systemd unit file at `/etc/systemd/system/craudinei.service`:

```ini
[Unit]
Description=Craudinei — Claude Code × Telegram bridge
After=network.target
Wants=network.target

[Service]
Type=simple
User=craudinei
Group=craudinei
WorkingDirectory=/home/craudinei
EnvironmentFile=/etc/craudinei/env
ExecStart=/usr/local/bin/craudinei -config /home/craudinei/.craudinei/craudinei.yaml
Restart=on-failure
RestartSec=5s
TimeoutStopSec=30s
StandardOutput=journal
StandardError=journal
SyslogIdentifier=craudinei

# Security hardening
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=read-only
ReadOnlyPaths=/etc/craudinei:/usr/local/bin
ReadWritePaths=/home/craudinei/.craudinei /var/log
RuntimeDirectory=craudinei
MountFlags=slave

[Install]
WantedBy=multi-user.target
```

Create a dedicated user (recommended):

```bash
useradd --system --create-home --home-dir /home/craudinei craudinei
mkdir -p /home/craudinei
chown craudinei:craudinei /home/craudinei
```

Create the environment file at `/etc/craudinei/env`:

```bash
# /etc/craudinei/env  — must have 0600 permissions
ANTHROPIC_API_KEY=sk-ant-...
TELEGRAM_BOT_TOKEN=123456789:ABCDEF...
CRAUDINEI_PASSPHRASE=your-secret-passphrase
```

Set correct permissions:

```bash
chmod 0600 /etc/craudinei/env
chown root:root /etc/craudinei/env
chmod 0600 /home/craudinei/.craudinei/craudinei.yaml
chown craudinei:craudinei /home/craudinei/.craudinei/craudinei.yaml
```

Place the config at `/home/craudinei/.craudinei/craudinei.yaml`:

```bash
mkdir -p /home/craudinei/.craudinei
cp your/craudinei.yaml /home/craudinei/.craudinei/craudinei.yaml
chown -R craudinei:craudinei /home/craudinei
```

Enable and start:

```bash
systemctl daemon-reload
systemctl enable craudinei
systemctl start craudinei
systemctl status craudinei
```

> **Note:** If `logging.file` or `logging.audit_file` is set to a path under `/var/log` (or any other writable directory), that directory must be added to `ReadWritePaths` in the unit file above. Without this, the process cannot write to the log file and will fail silently or error.

## Running with Docker

### Dockerfile

```dockerfile
FROM golang:1.22-alpine AS build
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /craudinei ./cmd/craudinei

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=build /craudinei /usr/local/bin/craudinei
# The approval server binds to 127.0.0.1 with an ephemeral port (not 8080).
# No ports need to be exposed for normal operation.
ENTRYPOINT ["/usr/local/bin/craudinei"]
```

Build and run:

```bash
docker build -t craudinei:latest .
docker run \
  --name craudinei \
  --restart unless-stopped \
  -v /path/to/craudinei.yaml:/home/craudinei/.craudinei/craudinei.yaml:ro \
  -v /path/to/session.json:/home/craudinei/.craudinei/session.json \
  -e ANTHROPIC_API_KEY \
  -e TELEGRAM_BOT_TOKEN \
  -e CRAUDINEI_PASSPHRASE \
  craudinei:latest
```

Note: The approval server inside the container needs to be reachable on `127.0.0.1`. If Claude Code is running on the host (not in the container), you need to expose the approval port from the container to the host — though in typical deployments both Craudinei and Claude Code run on the same host.

## Environment variables for secrets

Craudinei reads these from the environment directly (not from the config file):

| Variable | Required | Description |
|----------|----------|-------------|
| `ANTHROPIC_API_KEY` | Yes (direct env var) | Anthropic API key for Claude Code |
| `TELEGRAM_BOT_TOKEN` | Yes (via ${VAR} expansion in config YAML) | Telegram bot token |
| `CRAUDINEI_PASSPHRASE` | Yes (via ${VAR} expansion in config YAML) | Auth passphrase |

Best practice: store secrets in an environment file (as shown above) rather than inline in the config or systemd unit.

## Log management

Craudinei produces two log streams:

### Operational logs

Controlled by `logging.level` and `logging.file` in the config:
- `logging.level`: `debug`, `info`, `warn`, `error`
- `logging.file`: path; empty means stdout

When running under systemd, operational logs go to the journal. Use `journalctl -u craudinei -f` to follow.

### Audit logs

Always on, cannot be disabled. Controlled by `logging.audit_file`:
- Empty (`""`) → stderr
- Path → written to file

Audit logs record authentication attempts, commands, tool decisions, and session events. These are the primary source for security monitoring.

### Log rotation

For file-based logs, use `logrotate`:

```
/var/log/craudinei.log {
  daily
  rotate 7
  compress
  missingok
  notifempty
  postrotate
    systemctl reload craudinei > /dev/null 2>&1 || true
  endscript
}

/var/log/craudinei-audit.log {
  daily
  rotate 14
  compress
  missingok
  notifempty
  postrotate
    systemctl reload craudinei > /dev/null 2>&1 || true
  endscript
}
```

For journald, rely on systemd's built-in journal rotation (`SystemMaxFileSize`, `MaxRetentionSec`).

## Monitoring and health checks

### What to watch

- **Audit log** — Monitor for authentication failures (`auth: failure`), unexpected user IDs, and tool denials. Shutdown notifications are sent to the user via Telegram before the process terminates.
- **Operational log** — Watch for `session: crashed`, `subprocess: exit`, and `approval: timeout` entries.
- **Process uptime** — A Craudinei that restarts frequently indicates problems.
- **Disk space** — Audit and operational logs can grow; ensure log rotation is configured.

### Simple health check

```bash
# Check if the process is running and not crashed
systemctl is-active craudinei

# Check recent logs for errors
journalctl -u craudinei --since "5 minutes ago" -p err
```

## Troubleshooting common issues

### Bot does not respond to messages

1. Verify the bot token is correct and the bot is started (`systemctl status craudinei`).
2. Check that your user ID is in `allowed_users`.
3. Check `journalctl -u craudinei` for authentication or rate-limiting logs.

### "No active session" when calling `/begin`

1. Confirm Claude Code binary path is absolute and the file exists.
2. Verify the working directory is within `allowed_paths`.
3. Check `ANTHROPIC_API_KEY` is set; the subprocess will fail to start without it.

### Config reload fails

The `/reload` command only reloads non-sensitive fields. Check the Telegram chat for the specific validation error. The previous config stays active.

### Session stalls or hangs

- Check `inactivity_timeout` (default 15m) — the bot warns but does not auto-kill.
- The `progress_interval` (default 2m) sends updates when no events arrive.
- Use `/status` to see current state and uptime.

### Approval timeouts

- Default per-tool timeout is 5 minutes (`approval_timeout`, or tool-specific `timeout_bash`, etc.).
- Use the snooze button (`+5 min`) to extend.
- Timed-out approvals are auto-denied and the message is edited to show "(Timed out — auto-denied)".

### Orphan Claude Code process on restart

Craudinei checks for a PID file at `/tmp/craudinei.pid` on startup. If a stale PID exists, it kills the orphan process group before starting. This is normal behavior.

## Security hardening checklist

- [ ] Config file has `0600` permissions
- [ ] Secrets stored as `${VAR}` in config, not inline
- [ ] Environment file (`/etc/craudinei/env`) has `0600` permissions
- [ ] Dedicated system user (`craudinei`) runs the service, not `root`
- [ ] `NoNewPrivileges=true` in systemd unit
- [ ] `PrivateTmp=true` in systemd unit
- [ ] `ProtectSystem=strict` in systemd unit
- [ ] `ProtectHome=read-only` in systemd unit
- [ ] Bot is private (not public) — do not use `/setpublic` BotFather command
- [ ] `allowed_users` contains only your Telegram user ID(s)
- [ ] Audit log rotation is configured
- [ ] Claude Code runs on the same host as Craudinei (no network exposure for approval server)
- [ ] `ANTHROPIC_API_KEY` is set in the environment, not in the config file

## Updating

1. Pull the latest code or rebuild:
   ```bash
   cd /path/to/craudinei
   git pull
   make build
   ```

2. Stop the service:
   ```bash
   systemctl stop craudinei
   ```

3. Replace the binary:
   ```bash
   cp ./craudinei /usr/local/bin/craudinei
   ```

4. Restart:
   ```bash
   systemctl start craudinei
   systemctl status craudinei
   ```

Check the [CHANGELOG](CHANGELOG.md) for breaking changes between versions.
