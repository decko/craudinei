# Craudinei — Claude Code via Telegram

## Overview

Craudinei is a Go binary that bridges Claude Code sessions with Telegram. It enables two use cases:

1. **Remote coding assistant** — Send prompts from Telegram to a Claude Code session running on your dev machine or server, see outputs, approve destructive tool calls via inline buttons.
2. **Monitoring hub** — Monitor long-running Claude Code sessions with notifications for completions, errors, approval requests, and periodic progress updates.

### Security disclaimer

Bot chats with Telegram are **not end-to-end encrypted**. All prompts, code, file contents, and tool outputs flow through Telegram's servers in cleartext. Telegram staff or anyone who compromises Telegram's infrastructure can see everything. This tool is suitable for personal/hobby use with non-sensitive code. Do not use it with proprietary, classified, or secret source code.

## Architecture

Single Go binary with four components:

```
Telegram <-> Bot <-> Event Router <-> Session Manager <-> claude CLI (subprocess)
                         ^
                         |
                   Approval Server
                   (HTTP, localhost)
```

- **Telegram Bot** — Handles messages/commands, sends responses, renders inline approve/deny buttons. Uses a per-chat send queue with rate limiting (~1 msg/sec) to respect Telegram API limits.
- **Session Manager** — Spawns and manages a Claude Code subprocess, owns stdin/stdout/lifecycle. Manages an input queue to ensure one-prompt-per-completed-turn discipline.
- **Event Router** — Parses Claude Code's NDJSON stream, classifies events, routes them to the Telegram Bot via a bounded output channel. Coalesces rapid text events (500ms debounce window).
- **Approval Server** — A localhost-only HTTP server that Claude Code calls via `--permission-prompt-tool` to request tool approval. Routes the request to Telegram as inline buttons and blocks until the user responds or a timeout fires.

### Approach chosen

Direct Subprocess (over PTY/tmux scraping or WebSocket proxy). Structured JSON output makes parsing reliable and gives full lifecycle control.

### Concurrency model

All goroutines are rooted in a single `context.Context` created in `main()`. Cancelling the root context triggers graceful shutdown of all components.

```
main()
├── ctx, cancel = context.WithCancel(context.Background())
├── signal handler (SIGINT/SIGTERM) → cancel()
├── Telegram poller goroutine (ctx)
├── Telegram send queue goroutine (ctx) — drains outbound channel, rate-limited
├── Session (when active):
│   ├── stdout reader goroutine (ctx) — reads NDJSON, writes to event channel
│   ├── stdin writer goroutine (ctx) — drains input queue, writes one prompt per turn
│   ├── stderr reader goroutine (ctx) — logs stderr via slog
│   ├── progress ticker goroutine (ctx) — periodic notifications
│   └── approval server goroutine (ctx) — localhost HTTP for permission prompts
└── process reaper — on ctx cancel: SIGINT → 5s wait → SIGKILL process group
```

All shared state in `SessionState` is protected by `sync.Mutex`. Communication between goroutines uses typed Go channels. The event channel between router and bot is bounded (capacity 100) to provide backpressure.

### Graceful shutdown

When Craudinei receives SIGTERM/SIGINT:

1. Cancel root `context.Context`.
2. Send SIGINT to Claude Code process group (via `syscall.Kill(-pgid, syscall.SIGINT)`).
3. Wait for stdout reader to drain (via `sync.WaitGroup`).
4. Call `cmd.Wait()` only after all pipe readers complete (avoids the `StdoutPipe`/`Wait` deadlock).
5. Send "Craudinei shutting down" to Telegram.
6. Shut down approval server.
7. Exit.

Process group kill (`Setpgid: true` on subprocess) ensures child processes (e.g., Bash tool running `npm install`) are also terminated.

## Telegram Bot Interface

### Commands

| Command | Description |
|---|---|
| `/begin <directory>` | Start a new Claude Code session in the given working directory |
| `/stop` | Stop the current session (with confirmation) |
| `/cancel` | Interrupt the current task without killing the session (sends SIGINT) |
| `/status` | Show session state, working directory, session ID, uptime, token usage, remaining budget |
| `/auth <passphrase>` | Authenticate for the current Telegram chat session |
| `/resume [session_id]` | Resume a previous session (no arg = most recent) |
| `/help` | List all available commands with syntax and descriptions |
| `/reload` | Reload config (non-sensitive fields only) |
| `/sessions` | List recent sessions with IDs, timestamps, and working directories |
| `/attach <pid_or_pipe>` | Attach to an existing Claude Code process (future) |

**Note:** `/start` is a Telegram reserved command (sent automatically when a user first opens the bot). We use `/begin` instead. A bare `/start` (no arguments) is handled as a welcome/help message.

**Command registration:** On startup, the bot calls `setMyCommands` to register all commands with Telegram for autocomplete in the message input area. `/attach` is excluded until implemented.

**Command guards:** Commands are validated against the current session state. Invalid commands receive a descriptive error:

| State | Valid commands | Invalid → error message |
|---|---|---|
| `idle` | `/begin`, `/resume`, `/status`, `/help`, `/sessions` | `/stop` → "No active session" |
| `starting` | `/status`, `/help` | `/begin` → "Session is starting, please wait" |
| `running` | `/stop`, `/cancel`, `/status`, `/help` | `/begin` → "Session already active. `/stop` first" |
| `waiting_approval` | `/stop`, `/cancel`, `/status`, `/help` + approval buttons | `/begin` → "Session active, waiting for approval" |
| `stopping` | `/status`, `/help` | `/begin` → "Session is stopping, please wait" |
| `crashed` | `/begin`, `/resume`, `/status`, `/help`, `/sessions` | `/stop` → "Session already ended" |

### Message handling

- Once authenticated and a session is active, plain text messages are forwarded as prompts to Claude Code via the **input queue**.
- The bot only responds to whitelisted Telegram user IDs, and only after `/auth` succeeds.
- Messages sent before auth: the welcome screen is shown with an "Authenticate" button (see Rich UI section). Users can also type `/auth <passphrase>` directly.
- Messages sent with no active session: "No active session. Use `/begin <directory>` to start one."
- Messages sent while Claude is processing: queued (max depth 5). User notified: "Your message is queued and will be sent after the current task completes."
- Messages sent while waiting for approval: queued.
- `/begin` with no argument uses `default_workdir` from config.

### Input queue

A Go channel (`chan string`, capacity 5) sits between the Telegram handler and the stdin writer goroutine. The stdin writer drains one message at a time, writing to Claude Code's stdin only after the previous assistant turn completes (signaled by the event router observing a `result` or next `assistant` event). If the queue is full, the user receives: "Queue full. Please wait for Claude to finish."

### Stdin JSON protocol

Messages are sent to Claude Code's stdin as NDJSON (one JSON object per line):

```json
{"type": "user_message", "content": "your prompt here"}
```

For rich content (e.g., if Telegram photo support is added later):

```json
{"type": "user_message", "content": [{"type": "text", "text": "Review this"}]}
```

All user input is sanitized before JSON encoding: control characters stripped, length capped at 4096 chars (Telegram's own limit), and the JSON is built via `json.Marshal` (never string interpolation) to prevent JSON injection.

**Multi-turn behavior:** The `-p` flag alone is single-turn (process exits after one response). Combined with `--input-format stream-json`, the process stays alive reading JSON messages from stdin until EOF. The spec relies on this combination for multi-turn. The process lifetime is governed by stdin remaining open.

### Tool approval UX

When Claude Code requests approval for a destructive tool, the bot sends a message with context for the user to make an informed decision:

**Rendering strategy by tool type:**

| Tool | Message content |
|---|---|
| `Bash` | Tool name + first 10 lines of command. If longer, truncated with "(N more lines — reply 'show' to see full)" |
| `Edit` | File path + old_string/new_string as a mini diff. Truncated if long |
| `Write` | File path + line count + first 10 lines of content |
| Other | Tool name + truncated input |

**Inline keyboard:** `[Run this] [Skip] [+5 min]`

- Tool-specific labels instead of generic Approve/Deny.
- `[+5 min]` snooze button extends the timeout.
- A reminder notification is sent 1 minute before timeout.
- Default timeout: 5 minutes (configurable per-tool in config).
- On timeout: auto-deny, edit the message via `editMessageReplyMarkup` to remove buttons, append "(Timed out — auto-denied)" via `editMessageText`.

**Callback data format:** Short prefix + index into in-memory map. Example: `a:7` (approve, index 7). Keeps within Telegram's 64-byte callback data limit.

**Idempotency:** The `PendingApproval` field is guarded by `sync.Mutex` with a `handled bool` flag. First callback is processed; subsequent callbacks receive `answerCallbackQuery` with "Already handled." The approval message is edited to show the decision (e.g., "Approved" / "Skipped") to prevent confusion.

**Every `callbackQuery` must be answered** with `answerCallbackQuery` within 30 seconds — including expired/unknown callbacks (responded with `show_alert: true` and "This approval has expired"). This is a Telegram platform requirement.

### Output rendering

- All messages use `parse_mode: "HTML"` (not MarkdownV2). HTML is far more robust for arbitrary code output — only `<`, `>`, and `&` need escaping (to `&lt;`, `&gt;`, `&amp;`). Code blocks use `<pre><code class="language-go">...</code></pre>` for syntax highlighting.
- An initial `--append-system-prompt` instructs Claude to keep responses under 4000 characters and wrap lines at 80 characters.
- **Edit-based streaming:** When an assistant response begins, the bot sends an initial message ("Working..."). As text events arrive, it calls `editMessageText` to update the same message (debounced to ~1 edit/sec to respect rate limits). When the response is complete, a final edit delivers the full text. If the final text exceeds 4096 chars, overflow is sent as a new message.
- **Fallback chunking:** Splits at paragraph or code block boundaries, never mid-tag. Each chunk is valid HTML (open/close `<pre>` tags properly). If a single code block exceeds 4096 chars, it is split at line boundaries with `<pre>` re-opened in each chunk.
- **Very large outputs (>16K chars):** Sent as a `.txt` file attachment via `sendDocument` instead of flooding the chat.
- Bot responses use `reply_to_message_id` referencing the user's original prompt message for visual threading.
- Progress updates and auto-approved tool notifications use `disable_notification: true` to reduce mobile noise.
- While Claude is processing, `sendChatAction("typing")` is sent every 4 seconds.

### `/stop` confirmation

`/stop` is destructive. The bot responds with: "Stop the current session? [Yes, stop] [Cancel]" using inline keyboard buttons. Only on confirmation does the session terminate.

## Rich UI

Craudinei uses Telegram's `sendPhoto` with inline keyboards to create a polished, app-like experience. Key screens use banner images + structured button grids instead of plain text.

### Welcome screen

Displayed on first contact (`/start` with no arguments) or when an unauthenticated user messages the bot. Uses `sendPhoto` with a branded Craudinei banner:

```
┌─────────────────────────────┐
│                             │
│     [Craudinei banner]      │
│     Claude Code × Telegram  │
│                             │
├─────────────────────────────┤
│ Welcome to Craudinei!       │
│ Remote Claude Code sessions │
│ from your Telegram.         │
│                             │
│ Authenticate to get started.│
├─────────────────────────────┤
│  [Authenticate]   [Help]   │
└─────────────────────────────┘
```

- The "Authenticate" button sends a `callbackQuery` that prompts: "Send your passphrase now." The next plain-text message is treated as the passphrase (deleted immediately). This avoids the user having to type `/auth <passphrase>` and is more mobile-friendly.
- The "Help" button expands inline to show all commands.

### Post-auth home screen

After successful authentication, the bot sends a home screen:

```
┌─────────────────────────────┐
│                             │
│     [Craudinei banner]      │
│     Authenticated           │
│                             │
├─────────────────────────────┤
│ Ready to code. Choose a     │
│ project or start a session. │
├─────────────────────────────┤
│ [craudinei]  [api-server]  │
│ [frontend]   [infra]       │
│ [Other directory...]       │
│─────────────────────────────│
│ [Resume last]    [Sessions] │
│ [Status]         [Help]    │
└─────────────────────────────┘
```

- **Project buttons** — Auto-generated from subdirectories of `allowed_paths`. Each button starts a session in that directory (equivalent to `/begin <path>`). Only first-level subdirectories are shown (max 8, sorted by most recently modified). Keeps the grid clean on mobile.
- **"Other directory..."** — Opens a prompt: "Send the full directory path." The next plain-text message is used as the `/begin` argument. Validated against `allowed_paths`.
- **"Resume last"** — Resumes the most recent session (equivalent to `/resume` with no args).
- **"Sessions"** — Shows a list of recent sessions as buttons (see below).

### Directory picker

When the user taps "Other directory..." or sends `/begin` with no argument and there are multiple `allowed_paths`, the bot shows a picker:

```
┌─────────────────────────────┐
│ Choose a project directory: │
├─────────────────────────────┤
│ /home/user/dev              │
│ [craudinei]  [api-server]  │
│ [frontend]   [infra]       │
│─────────────────────────────│
│ /home/user/work             │
│ [backend]    [mobile-app]  │
│ [docs]                     │
│─────────────────────────────│
│ [Type a path...]  [Cancel] │
└─────────────────────────────┘
```

- Directories are grouped by their parent `allowed_path`.
- Button layout: 2-3 buttons per row, max 3 rows per `allowed_path` group. Fits well on mobile screens.
- Callback data: `d:<index>` mapping to the full path in memory (stays within 64-byte limit).

### Sessions list

`/sessions` or the "Sessions" button shows recent sessions as interactive buttons:

```
┌─────────────────────────────┐
│ Recent sessions:            │
├─────────────────────────────┤
│ [craudinei — 2h ago]       │
│ [api-server — yesterday]   │
│ [frontend — 3 days ago]    │
│─────────────────────────────│
│ [Back to home]             │
└─────────────────────────────┘
```

- Each button resumes that session (equivalent to `/resume <session_id>`).
- Shows project name (basename of workdir) + relative timestamp.
- Max 5 recent sessions shown.

### Active session status bar

When a session is started, the bot pins a status message to the top of the chat and updates it in-place via `editMessageText`:

```
┌─────────────────────────────┐
│ Session active              │
│ Project: craudinei          │
│ Status: running             │
│ Uptime: 12m · $0.42 / $5   │
│ Tokens: 3,200 in · 1,100 out│
├─────────────────────────────┤
│ [Cancel task]     [Stop]   │
└─────────────────────────────┘
```

- Updated every 30 seconds while the session is active (via `editMessageText`, no new messages).
- Shows live cost tracking and token usage.
- Buttons provide quick access to `/cancel` and `/stop` without typing.
- On session end, updated to final state: "Session ended · $1.23 · 45 turns"

### Approval cards

Tool approval requests are rendered as rich cards with structured information:

**Bash command:**
```
┌─────────────────────────────┐
│ Tool: Bash                  │
│─────────────────────────────│
│ <pre>make build &&          │
│ make test</pre>             │
│─────────────────────────────│
│ [Run this]  [Skip]  [+5m]  │
│ [Show full command]         │
└─────────────────────────────┘
```

**File edit:**
```
┌─────────────────────────────┐
│ Tool: Edit                  │
│ File: internal/bot/auth.go  │
│─────────────────────────────│
│ <pre>- oldLine              │
│ + newLine</pre>             │
│─────────────────────────────│
│ [Apply edit]  [Skip]  [+5m]│
│ [Show full diff]            │
└─────────────────────────────┘
```

**File write:**
```
┌─────────────────────────────┐
│ Tool: Write                 │
│ File: config/defaults.go    │
│ Lines: 47                   │
│─────────────────────────────│
│ <pre>package config         │
│ // first 10 lines...</pre>  │
│─────────────────────────────│
│ [Write file]  [Skip]  [+5m]│
│ [Show full content]         │
└─────────────────────────────┘
```

- "Show full" buttons send the complete content as a follow-up message (or as a `.txt` file if very large).
- After a decision, the card is edited to show the outcome: buttons replaced with "Approved" or "Skipped" text.
- Tool-specific action labels ("Run this" / "Apply edit" / "Write file") for clarity.

### Banner asset

The Craudinei banner is a static PNG stored at `assets/banner.png` in the project. It is sent via `sendPhoto` using a `file_id` after the first upload (Telegram caches the file and returns a reusable `file_id`, avoiding re-upload on every display).

Dimensions: 800x400px (2:1 ratio works well on both mobile and desktop Telegram). The banner shows the project name and a visual that communicates "code + chat."

### Navigation pattern

All rich UI screens follow a consistent pattern:

1. **Banner + context text** — What screen you're on and what's happening.
2. **Action buttons** — Primary actions in the top row (2-3 buttons).
3. **Navigation buttons** — "Back to home" / "Cancel" in the bottom row.
4. **Callback routing** — All button taps are routed through a single `callbackQuery` handler that dispatches based on prefix: `d:` (directory), `s:` (session), `a:` (approval), `n:` (navigation), `c:` (confirm).

Screens are updated in-place via `editMessageText` + `editMessageReplyMarkup` when possible (e.g., navigating from home → directory picker), avoiding chat clutter.

## Approval Server

The approval flow cannot use `--permission-mode default` in `-p` mode because non-interactive mode auto-denies tools not in `--allowedTools` — there is no stdin message type for approve/deny in the `stream-json` protocol.

Instead, Craudinei runs a **localhost-only HTTP server** that acts as a `--permission-prompt-tool` endpoint:

1. Claude Code is launched with `--permission-prompt-tool craudinei_approval` which points to a local MCP server (configured via `--mcp-config`).
2. When Claude Code needs permission for a tool not in `--allowedTools`, it calls the MCP tool `craudinei_approval` with the tool name and input.
3. The approval server receives the HTTP request, formats the tool call, and sends it to Telegram as an inline keyboard message.
4. The HTTP handler blocks (with timeout) waiting for the user's response.
5. When the user taps a button, the callback handler signals the blocked HTTP handler via a channel.
6. The approval server returns the decision to Claude Code, which proceeds or skips accordingly.

**MCP config** (generated at startup, written to a temp file):

```json
{
  "mcpServers": {
    "craudinei_approval": {
      "type": "stdio",
      "command": "craudinei-mcp-shim",
      "args": ["--port", "{{approval_port}}"]
    }
  }
}
```

Alternatively, if `--permission-prompt-tool` accepts an MCP server name directly, the approval server can be a stdio-based MCP server spawned by Craudinei itself, communicating over pipes. The implementation should validate which approach Claude Code supports and use the simplest one.

**Fallback:** If the approval server approach proves too complex during implementation, a simpler alternative is to use `--dangerously-skip-permissions` combined with PreToolUse hooks that call back to the bot for approval before each tool execution. This sacrifices some safety (the tool technically has permission) but achieves the same UX.

## Session Manager

### Spawning Claude Code

```
claude -p \
  --input-format stream-json \
  --output-format stream-json \
  --verbose \
  --append-system-prompt "Keep each response under 4000 characters. Wrap lines at 80 characters." \
  --allowedTools "Read" "Grep" "Glob" \
  --permission-prompt-tool craudinei_approval \
  --mcp-config /tmp/craudinei-mcp.json \
  --max-turns 50 \
  --max-budget-usd 5.00 \
  --add-dir <workdir>
```

**Flag notes:**

- **No `--bare`** — Removed to preserve CLAUDE.md auto-discovery (project context), which is critical for the remote coding assistant use case. The tradeoff is slightly slower startup due to hook/plugin loading.
- **`-p` + `--input-format stream-json`** — This combination is what enables multi-turn. `-p` alone is single-turn (exits after one response). With `stream-json` input, the process stays alive reading from stdin until EOF.
- **`--add-dir <workdir>`** — Ensures Claude Code discovers CLAUDE.md files in the working directory even without `--bare`.
- **`ANTHROPIC_API_KEY`** — Must be set in the environment for the subprocess. The spec requires this env var to be present; startup fails with a clear error if missing.
- **No `LS` tool** — There is no standalone `LS` tool in Claude Code. Directory listing is done via `Bash(ls)` or `Glob`.

### Subprocess management

- **Stdin pipe** — Written to only by the stdin writer goroutine (drains the input queue). Uses `json.Marshal` for all JSON encoding.
- **Stdout pipe** — Read line-by-line by the stdout reader goroutine using `bufio.Scanner` with an expanded buffer: `scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)` (1MB max line size). Claude Code can produce large JSON lines with code blocks.
- **Stderr** — Set via `cmd.Stderr = slogWriter` (an `io.Writer` that logs to `slog`). No pipe needed, avoids an extra goroutine.
- **Working directory** — Set via `cmd.Dir` from the `/begin <directory>` command.
- **Process group** — `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}` so child processes are killed with the parent.
- **Session ID** — Captured from the `system/init` event, stored for `/resume` support.
- **Pipe draining** — A `sync.WaitGroup` tracks the stdout reader. `cmd.Wait()` is called only after `wg.Wait()` completes, preventing the `StdoutPipe`/`Wait` deadlock.
- **Stdin write errors** — If writing to stdin returns an error (process died), the stdin writer notifies Telegram and transitions to `crashed` state. No panic.

### Graceful session shutdown (`/stop`)

1. Confirm with user via inline buttons.
2. Transition to `stopping` state.
3. Send SIGINT to process group: `syscall.Kill(-pgid, syscall.SIGINT)`.
4. Wait up to 5 seconds for stdout reader to finish (via `WaitGroup` with timeout).
5. If still alive, `syscall.Kill(-pgid, syscall.SIGKILL)`.
6. Call `cmd.Wait()`.
7. Transition to `idle`.
8. Notify Telegram: "Session stopped."

### Crash recovery

- Unexpected exit detected by stdout reader reaching EOF or `cmd.Wait()` returning a non-zero exit code.
- Transition to `crashed` state.
- Notify Telegram with categorized message:
  - "Claude Code exited unexpectedly (exit code N). Use `/begin` to start a new session or `/resume` to attempt recovery."
- Session ID is preserved for `/resume`.
- No auto-restart (user decides).

### Orphan prevention

- On startup, Craudinei checks for a PID file (`/tmp/craudinei.pid`). If found and the PID is alive, kills the orphan process group.
- On subprocess start, writes PID to the PID file.
- On clean exit, removes the PID file.

### Session persistence

Minimal session state is persisted to `~/.craudinei/session.json`:

```json
{
  "session_id": "abc123",
  "work_dir": "/home/user/dev/project",
  "pid": 12345,
  "started_at": "2026-04-17T19:00:00Z"
}
```

This allows recovery after bot restart: `/resume` can spawn a new subprocess with `--resume <session_id>`.

**Resume caveats:** Resuming loads the full conversation history into context. For long sessions, this can consume significant tokens. The `/sessions` command shows session age to help users decide whether to resume or start fresh.

### State machine

```
idle ──/begin──> starting ──init event──> running ──/stop──> stopping ──> idle
                                            │                   
                                            ├──tool_approval──> waiting_approval ──response──> running
                                            │
                                            ├──unexpected exit──> crashed
                                            │
                                            └──/cancel──> running (SIGINT interrupts current task)

crashed ──/begin──> starting
crashed ──/resume──> starting
```

**Guard conditions:** Every state transition is validated. Commands invalid for the current state return an error message (see command guards table above).

### Inactivity watchdog

If no events arrive from Claude Code for 15 minutes (configurable), the session is assumed stalled. The bot sends "Session appears stalled — no activity for 15 minutes. Use `/stop` to end or `/cancel` to interrupt." No auto-kill; the user decides.

## Event Router

### Event-to-action mapping

| Event Type | Subtype/Condition | Action |
|---|---|---|
| `system` | `init` | Store session ID, notify "Session started in `<dir>`. Auto-approved tools: Read, Grep, Glob. Budget: $5.00" |
| `assistant` | contains `text` blocks | Stream to Telegram via `editMessageText` |
| `assistant` | contains `thinking` blocks | Silent — do not forward thinking content to Telegram |
| `assistant` | contains `tool_use`, tool in allowedTools | Update status to "working", no notification |
| `assistant` | contains `tool_use`, tool NOT in allowedTools | Routed via approval server (see Approval Server section) |
| `user` | `tool_result` | No action |
| `result` | `success` | Notify with turn cost + cumulative session cost + token summary |
| `result` | `error_max_turns` | "Reached max turns (50). Session paused." |
| `result` | `error_max_budget_usd` | "Budget limit ($5.00) reached. Session ended." |
| `result` | `error_during_execution` | Categorized error with actionable guidance (see Error templates) |
| `system` | `api_retry` | Notify only if attempt > 2: "API retry 3/10 — temporarily unavailable" |
| `rate_limit` | — | "Rate limited by Claude API. Retrying in Ns. No action needed." |
| `system` | `compact_boundary` | Silent, log only. Note: pending tool call IDs referencing pre-compaction messages may become stale |

**Unknown content block types** (e.g., future additions) are silently ignored and logged, not forwarded.

### Output buffering and backpressure

The event router writes to a bounded channel (`chan Event`, capacity 100). The Telegram send goroutine reads from this channel with rate limiting (~1 msg/sec). If the channel fills:

- Progress and status events are dropped (logged).
- Result and error events are never dropped — they block until space is available.
- When backpressure occurs, it is logged as a warning.

Text events from rapid assistant responses are coalesced: accumulated over a 500ms window and sent as a single Telegram message (or `editMessageText` update).

### Error message templates

Each error category has a template with (a) what happened, (b) whether it's auto-handled, and (c) what the user can do:

- **Rate limit:** "Rate limited by the API. Retrying automatically in 30s. No action needed."
- **Crash:** "Claude Code exited unexpectedly (exit code N). Use `/begin` to start a new session or `/resume <id>` to attempt recovery."
- **API error:** "API returned error: [message]. This is usually temporary. The session is still active."
- **Auth lockout:** "Too many failed attempts. Try again in N minutes."
- **Config reload error:** "Config reload failed: [validation error]. Previous config is still active."
- **Max turns:** "Reached the maximum number of turns (50). Start a new session to continue."
- **Budget exceeded:** "Budget limit ($5.00) reached. Session ended to prevent overspending."

### Periodic progress

A background goroutine (tied to session `context.Context`) checks `LastActivity` every 2 minutes (configurable). Notifications include last known activity from the event stream:

- "Still working (3m12s) — currently reading files... (1,200 tokens used)"
- "Still working (5m04s) — last action: ran grep across 12 files..."

**Escalation:** After the third consecutive "Still working..." with no new events, the interval doubles and the message changes to "Session may be stalled." The inactivity watchdog (15 min) handles truly stuck sessions.

**Immediate acknowledgment:** When a user prompt is received and forwarded to Claude, the bot sends an immediate "Working on it..." message (which is then replaced via `editMessageText` with the actual streaming response).

## Configuration

Single YAML config file (`craudinei.yaml`):

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

**Key design decisions:**

- **Secrets via env vars** — Token and passphrase referenced as `${VAR}`. Expanded at load time using targeted field expansion (not `os.ExpandEnv` on raw YAML, which would corrupt literal `$` characters in non-secret fields). After loading, expanded values are never logged.
- **Absolute path for claude binary** — Prevents PATH manipulation attacks. Config validation fails if a relative path is given.
- **`/reload` safety** — On reload, only non-sensitive fields (timeouts, prompt, progress_interval) are re-expanded. Token and passphrase changes require restart. If reload validation fails, the previous config remains active and the error is sent to Telegram.
- **Config file permissions** — Startup validates that `craudinei.yaml` has `0600` permissions (owner read/write only). Refuses to start if group- or world-readable.
- **Approval port** — `0` means auto-assign an ephemeral port. The port is only bound to localhost.

## Security

### Authentication layers

1. **Telegram user ID whitelist** — Messages from unknown user IDs receive a single rate-limited response: "You are not authorized to use this bot." (max 1 per hour per user ID, to avoid confirming the bot exists to scanners while helping admins diagnose misconfiguration). The unknown user ID is logged server-side.
2. **Passphrase authentication** — `/auth <passphrase>` once per chat session. The `/auth` message is immediately deleted via `deleteMessage`. If deletion fails (network error, race condition), a warning is logged and the user is told: "Could not delete your auth message. Please delete it manually." A confirmation message "Authenticated successfully. Send `/begin <directory>` to start a session." is sent and pushes the passphrase off-screen.
3. **Auth session idle timeout** — Default 1 hour (not 24h — this is shell-level access). Configurable, max 4 hours. After timeout, re-authentication required.
4. **Auth rate limiting** — 3 failures in 5 minutes locks out per-user-ID with exponential backoff: 30 min → 1 hour → 4 hours. User is told: "Too many failed attempts. Try again in N minutes." Counter resets after successful auth. All failed attempts are logged to the audit log.

**Known limitation:** The passphrase is transmitted as a Telegram message, visible to Telegram's servers, OS notification previews, and potentially cached on multiple devices before deletion. Users should use a non-sensitive passphrase. A future improvement could use TOTP or challenge-response authentication.

### Path validation

The `/begin <directory>` command validates the working directory with defense-in-depth:

1. `filepath.Clean()` the path (normalizes `..` components).
2. `filepath.EvalSymlinks()` to resolve symlinks.
3. `os.Stat()` to verify the resolved path is a directory.
4. `strings.HasPrefix(resolvedPath, allowedPath + "/")` for each path in `allowed_paths` (trailing slash prevents `/home/user/dev-secrets` matching `/home/user/dev`).

All four checks must pass. Failure returns an error to the user.

### Subprocess sandboxing

- **Tool approval** — Tools not in `allowed_tools` require explicit user approval via the approval server (see Approval Server section). Auto-denied on timeout.
- **`--max-turns` and `--max-budget-usd`** — Cap runaway sessions. Note: `--max-budget-usd` is a soft limit (in-flight requests may exceed it). Craudinei tracks cumulative cost from `result` events and alerts at 80% of budget.
- **No dangerous tool patterns** — Even when approved, the approval message shows the full tool input. Users should scrutinize `Bash` commands carefully, especially those involving network access, credential files, or package managers.
- **Resource limits** — The subprocess is launched with `ulimit` constraints: `RLIMIT_AS` (memory), `RLIMIT_NPROC` (fork limit), `RLIMIT_FSIZE` (file size). Values are configurable.

**Known limitation: Prompt injection.** Claude Code reads files as part of normal operation. A malicious file in the working directory could contain prompt injection payloads that cause Claude to request approval for innocuous-looking but destructive tool calls. The approval UX mitigates this by showing raw tool inputs, but users should be aware of this inherent risk of AI-assisted coding tools. Defense-in-depth: run the subprocess in a container or under a restricted user when working with untrusted codebases.

**Known limitation: Read access scope.** Even with `allowed_paths`, the `Read` tool (which is auto-approved) can access any file the process user can read. `allowed_paths` only constrains the working directory, not Claude Code's file access radius.

### Input sanitization

User input from Telegram is sanitized before being sent to Claude Code's stdin:

- Unicode control characters (U+0000–U+001F except newline/tab) are stripped.
- Length capped at 4096 characters (Telegram's own limit).
- JSON is built via `json.Marshal`, never string interpolation, to prevent JSON injection that could break the `stream-json` framing.

### Attack surface

- **Bot token** — Stored only in env vars, never logged, never in command-line args. The Telegram HTTP client is wrapped to redact the token from any error messages before logging. HTTP-level debug logging must never be enabled in production.
- **No open ports** — Uses Telegram long-polling, not webhooks. The approval server binds to localhost only.
- **Startup cleanup** — Calls `deleteWebhook` on startup to ensure no stale webhook interferes with long-polling.
- **Process isolation** — Stdin/stdout pipes are local to the process.
- **Config file** — Required `0600` permissions, validated at startup.

### Audit logging

A dedicated audit log (separate from operational logs, cannot be disabled) records:

- All authentication attempts (success/failure) with timestamp and user ID.
- All commands received and from whom.
- All tool calls — approved, denied, and timed out — with tool name and input summary.
- All session starts, stops, crashes, and resumes.
- All file paths accessed (from `tool_use` events).

Uses structured `slog` fields: `slog.With("user_id", id, "action", action, "target", target, "outcome", outcome)`.

### Session revocation

If a user's device is compromised while a session is active:

- **Kill switch file** — If `/tmp/craudinei-kill` exists, the bot immediately terminates all sessions and exits. Can be created via SSH or cron.
- **CLI revoke** — Running `craudinei --revoke` sends SIGTERM to the running bot (via PID file) after killing any active Claude Code subprocess.
- **Remote:** A second whitelisted Telegram user ID (configured as `admin_users` in config) can always send `/stop` without auth, for emergency access.

## Project Structure

```
craudinei/
├── cmd/
│   └── craudinei/
│       └── main.go              # Entry point, signal handling, context tree
├── internal/
│   ├── types/
│   │   └── types.go             # Shared types: Event, ToolCall, SessionState
│   ├── bot/
│   │   ├── bot.go               # Telegram bot setup, command routing, send queue
│   │   ├── handlers.go          # /begin, /stop, /status, /auth, /help, /cancel handlers
│   │   ├── auth.go              # User ID check, passphrase validation, rate limiting
│   │   ├── renderer.go          # HTML formatting, chunking, edit-based streaming
│   │   ├── screens.go           # Rich UI screens: welcome, home, directory picker, sessions
│   │   └── callbacks.go         # Unified callback query dispatcher (d:, s:, a:, n:, c: prefixes)
│   ├── claude/
│   │   ├── manager.go           # Spawn/stop/resume Claude Code subprocess, pipe management
│   │   ├── state.go             # Session state machine with mutex, transition guards
│   │   └── inputqueue.go        # Bounded input queue, one-prompt-per-turn
│   ├── router/
│   │   ├── router.go            # NDJSON parser, event classification, output buffer
│   │   └── events.go            # Event type definitions, thinking block filter
│   ├── approval/
│   │   ├── server.go            # Localhost HTTP/MCP server for permission prompts
│   │   └── handler.go           # Telegram ↔ HTTP bridge for approval flow
│   ├── config/
│   │   ├── config.go            # YAML loading, targeted env var expansion, defaults
│   │   └── validate.go          # Config validation, file permissions check
│   └── audit/
│       └── audit.go             # Structured audit logging
├── assets/
│   └── banner.png               # Craudinei banner image (800x400px, 2:1 ratio)
├── docs/
│   ├── config.md                # Full config reference
│   ├── deployment.md            # Systemd, env setup, log management
│   └── botfather-setup.md       # Step-by-step Telegram bot creation
├── craudinei.yaml.example       # Annotated example config
├── README.md                    # Quick start, commands, security overview
├── go.mod
├── go.sum
└── Makefile                     # build, run, test, test-race targets
```

### Dependencies

- `github.com/go-telegram/bot` — Modern Telegram bot library with native `context.Context` support (preferred over `telegram-bot-api/v5` which is unmaintained since 2022)
- `gopkg.in/yaml.v3` — Config parsing
- Standard library for everything else (os/exec, bufio, encoding/json, log/slog, net/http, sync, syscall)

## Testing Strategy

### Unit tests

- **Config** — Loading, targeted env var expansion (verify literal `$` is preserved), defaults, validation errors, file permission checks.
- **Auth** — User ID whitelist, passphrase validation, rate limiting with exponential backoff, session expiry.
- **Router** — Table-driven tests: each row in the event-to-action mapping table is a test case (input NDJSON line → expected action). Includes tests for unknown content block types, thinking blocks, truncated JSON lines.
- **Renderer** — HTML escaping, chunking at paragraph/code block boundaries, `<pre>` tag re-opening across chunks, very large output → file attachment threshold.
- **Screens** — Welcome screen button layout, directory picker generation from `allowed_paths`, sessions list rendering, status bar formatting, approval card rendering per tool type. Callback data stays within 64-byte limit.
- **State machine** — All valid transitions succeed. All invalid transitions return errors. Concurrent access under `go test -race`.
- **Input queue** — Queue full behavior, drain-after-turn-complete, prompt ordering.
- **Path validation** — Normal paths, symlinks, `..` traversal, prefix attacks, non-existent paths, non-directory paths.

### Integration tests

- **Session Manager** — Spawn a mock process (using `exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--")` pattern) that emits canned NDJSON. Test: start → receive events → send prompt → stop. Also test: process exits while stdin write is in-flight. Process produces lines larger than default scanner buffer. Process exits while approval is pending. Rapid `/stop` + `/begin` sequences (no goroutine leaks).
- **Bot + Router** — Mock Telegram HTTP server. Verify end-to-end: user sends message → Claude gets prompt → Claude responds → user sees response. Test Telegram rate limit handling (mock 429 responses).
- **Approval Server** — Verify MCP/HTTP handshake, timeout behavior, concurrent approval requests.

### Test requirements

- **`-race` flag mandatory** — `make test` runs `go test -race ./...`. The Makefile test target enforces this.
- **No live API calls** — All tests use mocks/fakes. The `claude.binary` config override enables testing with canned NDJSON scripts.

### Manual testing

- `craudinei.yaml.example` with `claude.binary` override pointing to a shell script that echoes canned NDJSON responses, enabling full-flow testing without API tokens.

## Documentation

The project ships with four documentation deliverables:

### README.md

The project root README covers:

- **What it is** — One-paragraph description of Craudinei.
- **Prerequisites** — Go 1.22+, Claude Code CLI installed, Telegram account, `ANTHROPIC_API_KEY`.
- **Quick start** — Five-step guide:
  1. Create a Telegram bot via BotFather (link + instructions).
  2. Copy `craudinei.yaml.example` to `craudinei.yaml`, fill in token and user ID.
  3. Set environment variables (`TELEGRAM_BOT_TOKEN`, `CRAUDINEI_PASSPHRASE`, `ANTHROPIC_API_KEY`).
  4. `make build && ./craudinei`
  5. Open the bot in Telegram, authenticate, start a session.
- **How to find your Telegram user ID** — Link to `@userinfobot` or instructions to use the Telegram API.
- **Commands reference** — Table of all bot commands with short descriptions.
- **Configuration** — Link to the full config reference.
- **Security considerations** — Short summary of the key limitations (Telegram cleartext, approval fatigue, prompt injection) with link to the full spec.
- **License** — TBD.

### Config reference (docs/config.md)

Complete reference for `craudinei.yaml`:

- Every field documented with: name, type, default value, description, and example.
- Grouped by section: `telegram`, `claude`, `approval`, `logging`.
- Env var expansion syntax explained (which fields support `${VAR}`, targeted expansion behavior).
- File permission requirements (`0600`).
- Hot-reload behavior: which fields can be reloaded via `/reload` and which require restart.

### Deployment guide (docs/deployment.md)

Covers running Craudinei in production:

- **Systemd service** — Complete `craudinei.service` unit file with:
  - `EnvironmentFile` for secrets (token, passphrase, API key).
  - `Restart=on-failure` with rate limiting.
  - `WorkingDirectory` and `User` directives.
  - Log routing to journald.
- **Environment setup** — How to create the env file with proper `0600` permissions, required variables.
- **Log management** — Where logs go (operational + audit), rotation recommendations.
- **Firewall** — No inbound ports needed (long-polling). Outbound HTTPS to `api.telegram.org` and `api.anthropic.com`.
- **Updates** — How to update Craudinei (rebuild, restart service) and Claude Code (check compatibility).
- **Monitoring** — Watch the audit log for auth failures; watch the operational log for subprocess crashes.
- **Backup** — `~/.craudinei/session.json` and `craudinei.yaml` are the only stateful files.

### BotFather setup guide (docs/botfather-setup.md)

Step-by-step Telegram bot creation:

1. Open `@BotFather` in Telegram.
2. Send `/newbot`, choose a name and username.
3. Copy the bot token → set as `TELEGRAM_BOT_TOKEN`.
4. Send `/setdescription` — set a short bot description.
5. Send `/setabouttext` — set the "About" text shown in the bot profile.
6. Send `/setuserpic` — upload the Craudinei banner as the bot's profile picture.
7. **Do NOT set a webhook** — Craudinei uses long-polling.
8. **Do NOT make the bot public** — Keep it private (no username listing) for security. Share the bot link only with whitelisted users.
9. Commands are registered automatically at startup via `setMyCommands` — no need to configure in BotFather.

### Inline documentation

- `craudinei.yaml.example` — Annotated example config with comments explaining every field, defaults, and security notes. Serves as a quick reference without reading the full docs.
- `/help` command — In-bot help shows all commands with syntax and descriptions.

## Known Limitations

1. **Telegram is not end-to-end encrypted for bots.** All content flows through Telegram's servers in cleartext.
2. **Approval fatigue.** Users will develop muscle memory for tapping "Run this." Defense-in-depth (tool blocklists, resource limits, containerization) compensates for this.
3. **Prompt injection.** Malicious file content can influence Claude's tool requests. Show raw tool inputs in approvals.
4. **Read access scope.** Auto-approved `Read` tool can access any file the process user can read, not just `allowed_paths`.
5. **`--max-budget-usd` is a soft limit.** In-flight requests may exceed it. Craudinei adds a secondary hard check.
6. **Mobile code readability.** Code blocks on small screens require horizontal scrolling for wide lines. The system prompt requests 80-char wrapping but Claude may not always comply.
7. **Linux/macOS only.** Process group management (`Setpgid`, `SIGINT`) does not work on Windows.

## Future work (out of scope)

- Multi-session support (session registry, per-session Telegram threads)
- `/attach` to existing Claude Code processes
- Webhook mode for deployment behind a reverse proxy (requires TLS, increases attack surface)
- Telegram group support with per-user sessions
- TOTP / challenge-response authentication (replaces plaintext passphrase)
- Message-level encryption for sensitive code (pre-shared key + companion app)
- Claude Code version pinning (verify supported CLI version at startup)
- Pinned status message updated in-place with session dashboard
- Container/VM sandboxing for untrusted codebases
