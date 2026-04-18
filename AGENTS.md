# Agents & AI-Assisted Development Conventions

This document defines conventions for AI-assisted development in this repository.
All human and AI contributors must follow these rules.

## Commit Conventions

### Assisted-by Trailer

Every commit made with AI assistance **must** include an `Assisted-by` trailer
identifying the model used. This goes in the commit message body, not the subject line.

```
feat: add event router with NDJSON parsing

Implement the event router that reads Claude Code's streaming JSON
output and classifies events into actionable types.

Assisted-by: Claude Opus 4.6 <noreply@anthropic.com>
```

If multiple models contributed to a single commit:

```
Assisted-by: Claude Opus 4.6 <noreply@anthropic.com>
Assisted-by: Claude Sonnet 4.6 <noreply@anthropic.com>
```

Commits made entirely by a human should **not** include this trailer.
Use your actual model name — do not write a literal `<model>` placeholder.

### Commit Message Format

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <subject>

<body>

<trailers>
```

**Types:** `feat`, `fix`, `refactor`, `test`, `docs`, `chore`, `ci`, `perf`

**Rules:**
- Subject line: imperative mood, lowercase, no period, max 72 chars
- Body: explain **why**, not what. The diff shows what.
- One logical change per commit. Don't bundle unrelated changes.
- Never commit generated files, secrets, or large binaries.

### Commit Hygiene

- **Atomic commits** — Each commit compiles and tests pass. No broken intermediate states.
- **No fixup chains** — If you find a bug in uncommitted work, fix it before committing. Don't create a commit then immediately fix it in another.
- **Prefer new commits over amending** — Amending rewrites history. Only amend if the commit hasn't been pushed.
- **Never force-push** — Not to main, not to any branch. If you need to fix a pushed commit, create a new commit.

## Branch & Worktree Management

### Branch Naming

**Manual development:**
```
<type>/<short-description>
```
Examples: `feat/event-router`, `fix/stdin-deadlock`, `refactor/config-loading`

**Automated dev-loop:**
```
task/<issue-number>-<short-name>
```
Examples: `task/1-scaffolding`, `task/6-state-machine`

### Git Worktrees

Use worktrees for all development. The main checkout is read-only.

```bash
# Manual: worktree alongside repo
git worktree add ../craudinei-feat-router -b feat/event-router

# Automated: worktree inside .worktrees/
git worktree add .worktrees/craudinei/task-1 -b task/1-scaffolding

# List active worktrees
git worktree list

# Clean up after merge
git worktree remove ../craudinei-feat-router
```

**Rules:**
- Manual worktrees live alongside the main repo: `../craudinei-<branch-short-name>`
- Automated worktrees live inside the repo: `.worktrees/craudinei/task-<issue-number>`
- Always clean up worktrees after merging. Run `git worktree list` before pruning.
- Each worktree should have its own independent build state.
- On session start, check `git worktree list` and clean up stale worktrees.

### Pull Requests

- PRs target `main` unless otherwise specified.
- PR title follows the same conventional commit format as commit subjects.
- Squash-merge for single-purpose PRs. Include the `Assisted-by` trailer in the
  PR body so it survives the squash (GitHub uses the PR body for the squash message).
- Delete the branch after merge.
- PR body must include: summary of changes, `Closes #<issue>`, and links to any
  triage issues created during review.

## Code Quality

### Go Conventions

- **Format:** `gofmt` (enforced by pre-commit hook and CI)
- **Lint:** `go vet` (enforced by pre-commit hook and CI)
- **Build:** `go build ./...` must succeed (enforced by pre-commit hook and CI)
- **Test:** `go test -race -count=1 ./...` must pass in CI before merging
- **Tidy:** Run `go mod tidy` after adding or removing dependencies
- **Naming:** Follow [Effective Go](https://go.dev/doc/effective_go) conventions
- **Comments:** Only when the *why* is non-obvious. No `// returns the name` noise.

### Error Handling

- Return errors, don't panic.
- Wrap errors at package boundaries: `fmt.Errorf("config: loading: %w", err)`
- Use `%w` when callers need `errors.Is`/`errors.As`. Use `%v` when informational.
- Check every returned error. No `_ = doSomething()`.

### context.Context

- Every function that performs I/O or may block accepts `context.Context` as first parameter.
- Never store contexts in structs. Pass them as function arguments.
- Cancel contexts to release resources. Use `defer cancel()`.

### Struct Initialization

- Export constructor functions for types that require initialization (mutexes, channels, required fields).
- Never rely on zero-value for types with mutexes or channels.
- Example: `func NewManager(...) *Manager { ... }`

### Interface Design

- Define interfaces at the consumer, not the producer.
- When a component depends on another, accept an interface for testability.
- Keep interfaces small — 1-3 methods.

### Dependency Injection

- All dependencies are wired in `main.go`.
- Packages must not import `config` to read configuration directly.
- Receive configuration values through constructors or function parameters.

### File Organization

- One package per directory. Package name matches directory name.
- Test files live next to the code they test: `foo.go` + `foo_test.go`.
- `internal/types/` is limited to cross-cutting data types (Event, SessionState).
  Do not add business logic to it.
- Keep files focused. If a file exceeds ~300 lines, consider splitting by responsibility.

### Testing

- **Table-driven tests** for functions with multiple input/output combinations.
  Name each subtest descriptively.
- **Test helpers** must call `t.Helper()` so failure messages report the caller's line.
- **Mocking strategy:** Prefer interface-based fakes over mock libraries. For subprocess
  testing, use shell script mocks in `testdata/`. For external API clients, define a narrow
  interface at the consumer and implement a fake in `_test.go`.
- **`testdata/` convention:** Test fixtures and mock scripts live in `testdata/` directories.
  These are ignored by `go build` automatically.
- **Unit vs integration:** Pure logic (types, config, router, audit, renderer, auth, state
  machine, input queue) uses TDD. Subprocess management and Telegram integration use
  integration tests with mock scripts or interface fakes.
- **Race detector:** Always test with `-race`. `make test` enforces this.

## Continuous Integration

This repository uses GitHub Actions for CI. The workflow is at `.github/workflows/ci.yml`.

### What CI checks

- `gofmt` formatting (all files, not just staged)
- `go vet` static analysis
- `go build ./...` compilation
- `go test -race -count=1 ./...` tests with race detector
- `go mod tidy` produces no diff

### CI as the gate of record

Pre-commit hooks provide fast local feedback but can be bypassed. CI is the authoritative
quality gate. A PR cannot be merged unless CI passes. Do not work around CI failures.

### Branch protection

`main` has branch protection requiring:
- Pull request before merging
- CI status checks to pass
- Linear history (squash merge)

## AI Agent Rules

### What Agents Must Do

- Follow all conventions in this file without exception.
- Read `AGENTS.md` at the start of every task (not just session start).
- Run `make lint` before committing (the pre-commit hook enforces this, but verify).
- Include the `Assisted-by` trailer with your actual model name in every commit.
- Use `make test` (which runs with `-race`) to verify changes.
- Run `go mod tidy` after adding or removing dependencies.
- Read existing code before modifying it. Follow established patterns.

### What Agents Must Not Do

- **Never** commit secrets, tokens, API keys, or credentials.
- **Never** skip hooks (`--no-verify`) or bypass signing.
- **Never** force-push to any branch.
- **Never** amend commits that have been pushed.
- **Never** create files outside the repository root.
- **Never** run destructive commands (`rm -rf`, `git clean -fdx`) without explicit
  human approval.
- **Never** modify `.git/config`, `.gitmodules`, or hook scripts directly. Use
  `make install-hooks` instead.
- **Never** run two automated dev-loop sessions simultaneously against the same repo.

### Context & Session Management

- Don't persist session-specific information in committed files.
- Don't create TODO/FIXME comments referencing conversations, tickets, or sessions.
  Those belong in the issue tracker.
- If a task is too large for one session, document the stopping point in the PR
  description, not in the code.
- Use GitHub issue state (`gh issue list --state closed`) as source of truth for
  what is complete — not your in-context memory. Context compaction will erase your
  memory of completed tasks.

## Hooks

This repository uses git hooks to enforce quality standards. Install them with:

```bash
make install-hooks email=brito.afa@gmail.com
```

### Installed Hooks

| Hook | What it does |
|------|-------------|
| `pre-commit` | Validates author email, runs `gofmt`, `go vet`, and `go build` |
| `commit-msg` | Validates conventional commit format |

### Bypassing Hooks

Don't. If a hook fails, fix the underlying issue. If a hook is wrong, fix the hook
via `make install-hooks` and commit the Makefile change.
