# Craudinei

Go binary that bridges Claude Code CLI sessions with Telegram.

Read and follow all conventions in [AGENTS.md](AGENTS.md).

## Key Files

- Spec: `docs/superpowers/specs/2026-04-17-craudinei-design.md`
- Plan: `docs/superpowers/plans/2026-04-17-craudinei-implementation.md`
- Module: `github.com/decko/craudinei`

## Quick Reference

```bash
make build          # Build binary
make test           # Run tests with -race
make lint           # Check gofmt + go vet
make install-hooks email=brito.afa@gmail.com  # Install git hooks
```
