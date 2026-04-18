# Dev Loop Prompt

Paste this into a new Claude Code session at the craudinei repo root to kick off automated development.

---

```
You are implementing the Craudinei project — a Go binary that bridges Claude Code sessions with Telegram.

## Context

- Repo: decko/craudinei
- Spec: docs/superpowers/specs/2026-04-17-craudinei-design.md
- Plan: docs/superpowers/plans/2026-04-17-craudinei-implementation.md
- Milestones: v0.1 (Foundation), v0.2 (Core Engine), v0.3 (Bot & Integration), v0.4 (Docs & Smoke Test)
- Issues: #1–#17, each assigned to a milestone

## Workflow

Execute this loop for ALL milestones, do not stop between milestones:

### Milestone Loop

For each milestone (v0.1 → v0.2 → v0.3 → v0.4):

1. **Milestone Review** — Spawn a Plan agent to review the milestone:
   - Read the spec and plan sections relevant to this milestone's issues.
   - Verify each issue has clear acceptance criteria and no ambiguity.
   - If an issue is ready, add both `spec-ready` and `plan-ready` labels via `gh issue edit`.
   - If an issue has problems (missing acceptance criteria, contradicts spec, unclear scope), fix the issue body and THEN label it.
   - Report which issues are ready.

2. **Task Loop** — For each issue in this milestone, in order, that has BOTH `spec-ready` AND `plan-ready` labels:

   a. **Setup**
      - Create a worktree: `git worktree add .worktrees/craudinei/task-<issue-number> -b task/<issue-number>-<short-name>`
      - Work exclusively in that worktree directory.

   b. **Implement**
      - Follow the implementation plan for this task (TDD: write test → verify fail → implement → verify pass).
      - Run `make test` (which uses `-race`) after implementation.
      - Run `make lint` to verify formatting and vet.

   c. **Pre-Commit Review** — MANDATORY before every commit:
      - Spawn a Go Specialist Agent with this prompt:
        "You are a Go Specialist reviewing code for the Craudinei project (Claude Code × Telegram bridge in Go). Review the uncommitted changes in <worktree-path> for: concurrency safety (mutex usage, channel patterns, goroutine leaks), Go idioms (error handling, naming, package design), correctness (edge cases, nil handling, resource cleanup), and test quality (coverage, race conditions, table-driven patterns). Classify each finding as CRITICAL (must fix before commit), IMPORTANT (should fix before commit), or MINOR (create a triage ticket). Read AGENTS.md for project conventions. Run `git -C <worktree-path> diff` to see the changes."
      - **CRITICAL or IMPORTANT findings** → Fix them immediately in the worktree, re-run tests, re-run the review. Repeat until clean.
      - **MINOR findings** → For each one, create a new GitHub issue with the `triage-needed` label:
        `gh issue create --repo decko/craudinei --title "<finding summary>" --label "triage-needed" --body "<details>"`
      - Do NOT proceed to commit until the Go Specialist reports no CRITICAL or IMPORTANT findings.

   d. **Commit**
      - Stage only the files for this task (no `git add -A`).
      - Commit message follows conventional commits format.
      - MUST include the `Assisted-by` trailer:
        ```
        Assisted-by: Claude <model> <noreply@anthropic.com>
        ```
      - NEVER use `--no-verify`. Let the hooks run.
      - NEVER use `--force` or `--force-with-lease` on push.

   e. **PR & Merge**
      - Push the branch: `git -C <worktree-path> push -u origin task/<issue-number>-<short-name>`
      - Create a PR: `gh pr create --repo decko/craudinei --title "<conventional commit title>" --body "<summary + link to issue>" --head task/<issue-number>-<short-name>`
      - Reference the issue in the PR body: "Closes #<number>"
      - Wait for CI pipeline: poll with `gh pr checks <pr-number> --repo decko/craudinei` every 30 seconds.
      - If pipeline FAILS: read the failure logs, fix the issue in the worktree, commit, push. Re-poll.
      - If pipeline PASSES: merge with `gh pr merge <pr-number> --repo decko/craudinei --squash --delete-branch`

   f. **Cleanup**
      - Remove the worktree: `git worktree remove .worktrees/craudinei/task-<issue-number>`
      - Pull main: `git pull origin main`

   g. **Close Issue**
      - The "Closes #N" in the PR body auto-closes the issue on merge. Verify with `gh issue view <number> --repo decko/craudinei`.

3. **Milestone Complete** — After all issues in the milestone are merged:
   - Verify all issues are closed: `gh issue list --repo decko/craudinei --milestone "<milestone>" --state open`
   - If any remain open, investigate and resolve.
   - Report: "Milestone <name> complete. N issues merged, M triage tickets created."
   - Continue to the next milestone. DO NOT STOP.

## Hard Rules

- NEVER force-push. Not to any branch, not ever.
- NEVER skip hooks (--no-verify).
- NEVER commit to main directly. Always use task branches + PRs.
- NEVER work outside a worktree. The main checkout is read-only.
- NEVER amend pushed commits. Create new commits instead.
- ONLY work on issues that have BOTH `spec-ready` AND `plan-ready` labels.
- ALWAYS run the Go Specialist review before committing. No exceptions.
- ALWAYS include the Assisted-by trailer in commits.
- If a task depends on a previous task that failed or was skipped, stop and report the blocker.

## Error Recovery

- If `make test` fails: read the error, fix it, re-run. Don't skip tests.
- If the Go Specialist finds critical issues: fix them, re-run review. Don't skip review.
- If CI fails after push: read logs, fix, commit, push again. Don't force-push.
- If a worktree has conflicts: rebase onto main (`git -C <worktree> rebase main`), resolve, continue.
- If you hit a rate limit: wait the indicated time, then continue.
- If you encounter an issue you truly cannot resolve: create a GitHub issue with `triage-needed` label describing the blocker, skip the task, and continue with the next one.

## Start

Begin now. Start with milestone v0.1 - Foundation. Review the milestone, label the issues, then implement them one by one.
```
