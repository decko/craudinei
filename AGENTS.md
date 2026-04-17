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
- **Never force-push to main** — If you need to fix a pushed commit, create a new commit.

## Branch & Worktree Management

### Branch Naming

```
<type>/<short-description>
```

Examples: `feat/event-router`, `fix/stdin-deadlock`, `refactor/config-loading`

### Git Worktrees

Use worktrees for parallel work streams. This repo encourages worktree-based development
to keep `main` clean and allow concurrent feature work.

```bash
# Create a worktree for a feature
git worktree add ../craudinei-feat-router feat/event-router

# List active worktrees
git worktree list

# Clean up after merge
git worktree remove ../craudinei-feat-router
```

**Rules:**
- Worktree directories live alongside the main repo, prefixed with the repo name:
  `../craudinei-<branch-short-name>`
- Always clean up worktrees after merging. Stale worktrees create confusion.
- Never run `git worktree prune` without checking `git worktree list` first.
- Each worktree should have its own independent build state. Run `go build` in the
  worktree, not in the main repo.

### Pull Requests

- PRs target `main` unless otherwise specified.
- PR title follows the same conventional commit format as commit subjects.
- Squash-merge for single-purpose PRs. Merge commit for multi-commit PRs where
  history matters.
- Delete the branch after merge.

## Code Quality

### Go Conventions

- **Format:** `gofmt` (enforced by pre-commit hook)
- **Lint:** `go vet` (enforced by pre-commit hook)
- **Build:** `go build ./...` must succeed (enforced by pre-commit hook)
- **Test:** `go test -race ./...` must pass before pushing
- **Naming:** Follow [Effective Go](https://go.dev/doc/effective_go) conventions
- **Errors:** Return errors, don't panic. Wrap with `fmt.Errorf("context: %w", err)`
- **Comments:** Only when the *why* is non-obvious. No `// returns the name` noise.

### File Organization

- One package per directory. Package name matches directory name.
- Test files live next to the code they test: `foo.go` + `foo_test.go`.
- Shared types go in `internal/types/`. Don't create circular imports.
- Keep files focused. If a file exceeds ~300 lines, consider splitting by responsibility.

## AI Agent Rules

### What Agents Must Do

- Follow all conventions in this file without exception.
- Run `make lint` before committing (the pre-commit hook enforces this, but verify).
- Include the `Assisted-by` trailer in every commit.
- Use `make test` (which runs with `-race`) to verify changes.
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

### Context & Session Management

- Don't persist session-specific information in committed files.
- Don't create TODO/FIXME comments referencing conversations, tickets, or sessions.
  Those belong in the issue tracker.
- If a task is too large for one session, document the stopping point in the PR
  description, not in the code.

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
