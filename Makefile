.PHONY: build run test lint fmt vet clean install-hooks

BINARY = craudinei
BUILD_DIR = ./cmd/craudinei

build:
	go build -o $(BINARY) $(BUILD_DIR)

run: build
	./$(BINARY)

test:
	go test -race -v ./...

lint: fmt vet
	@echo "Lint passed."

fmt:
	@echo "Checking gofmt..."
	@test -z "$$(gofmt -l . 2>/dev/null)" || { echo "Files need formatting:"; gofmt -l .; exit 1; }

vet:
	go vet ./...

clean:
	rm -f $(BINARY)

# Install git hooks. Requires email parameter.
# Usage: make install-hooks email=brito.afa@gmail.com
install-hooks:
ifndef email
	$(error Usage: make install-hooks email=you@example.com)
endif
	@mkdir -p .git/hooks
	@echo '#!/bin/bash' > .git/hooks/pre-commit
	@echo 'set -euo pipefail' >> .git/hooks/pre-commit
	@echo '' >> .git/hooks/pre-commit
	@echo 'ALLOWED_EMAIL="$(email)"' >> .git/hooks/pre-commit
	@echo '' >> .git/hooks/pre-commit
	@echo '# --- Email gate ---' >> .git/hooks/pre-commit
	@echo 'AUTHOR_EMAIL=$$(git config user.email)' >> .git/hooks/pre-commit
	@echo 'if [ "$$AUTHOR_EMAIL" != "$$ALLOWED_EMAIL" ]; then' >> .git/hooks/pre-commit
	@echo '  echo "ERROR: commits must use email $$ALLOWED_EMAIL (got $$AUTHOR_EMAIL)"' >> .git/hooks/pre-commit
	@echo '  echo "Run: git config user.email $$ALLOWED_EMAIL"' >> .git/hooks/pre-commit
	@echo '  exit 1' >> .git/hooks/pre-commit
	@echo 'fi' >> .git/hooks/pre-commit
	@echo '' >> .git/hooks/pre-commit
	@echo '# --- Go format check ---' >> .git/hooks/pre-commit
	@echo 'if command -v gofmt &>/dev/null; then' >> .git/hooks/pre-commit
	@echo '  STAGED_GO=$$(git diff --cached --name-only --diff-filter=ACM | grep "\.go$$" || true)' >> .git/hooks/pre-commit
	@echo '  if [ -n "$$STAGED_GO" ]; then' >> .git/hooks/pre-commit
	@echo '    UNFORMATTED=$$(gofmt -l $$STAGED_GO 2>/dev/null || true)' >> .git/hooks/pre-commit
	@echo '    if [ -n "$$UNFORMATTED" ]; then' >> .git/hooks/pre-commit
	@echo '      echo "ERROR: files need gofmt:"' >> .git/hooks/pre-commit
	@echo '      echo "$$UNFORMATTED"' >> .git/hooks/pre-commit
	@echo '      echo "Run: gofmt -w $$UNFORMATTED"' >> .git/hooks/pre-commit
	@echo '      exit 1' >> .git/hooks/pre-commit
	@echo '    fi' >> .git/hooks/pre-commit
	@echo '  fi' >> .git/hooks/pre-commit
	@echo 'fi' >> .git/hooks/pre-commit
	@echo '' >> .git/hooks/pre-commit
	@echo '# --- Go vet ---' >> .git/hooks/pre-commit
	@echo 'if command -v go &>/dev/null && [ -f go.mod ]; then' >> .git/hooks/pre-commit
	@echo '  go vet ./... 2>&1 || { echo "ERROR: go vet failed"; exit 1; }' >> .git/hooks/pre-commit
	@echo 'fi' >> .git/hooks/pre-commit
	@echo '' >> .git/hooks/pre-commit
	@echo '# --- Go build ---' >> .git/hooks/pre-commit
	@echo 'if command -v go &>/dev/null && [ -f go.mod ]; then' >> .git/hooks/pre-commit
	@echo '  go build ./... 2>&1 || { echo "ERROR: go build failed"; exit 1; }' >> .git/hooks/pre-commit
	@echo 'fi' >> .git/hooks/pre-commit
	@echo '' >> .git/hooks/pre-commit
	@echo '# --- No secrets ---' >> .git/hooks/pre-commit
	@echo 'STAGED=$$(git diff --cached --name-only --diff-filter=ACM)' >> .git/hooks/pre-commit
	@echo 'for f in $$STAGED; do' >> .git/hooks/pre-commit
	@echo '  case "$$f" in' >> .git/hooks/pre-commit
	@echo '    *.env|*.pem|*.key|credentials*|*secret*)' >> .git/hooks/pre-commit
	@echo '      echo "ERROR: refusing to commit potential secret: $$f"' >> .git/hooks/pre-commit
	@echo '      exit 1' >> .git/hooks/pre-commit
	@echo '      ;;' >> .git/hooks/pre-commit
	@echo '  esac' >> .git/hooks/pre-commit
	@echo 'done' >> .git/hooks/pre-commit
	@chmod +x .git/hooks/pre-commit
	@echo '#!/bin/bash' > .git/hooks/commit-msg
	@echo 'set -euo pipefail' >> .git/hooks/commit-msg
	@echo '' >> .git/hooks/commit-msg
	@echo 'MSG_FILE="$$1"' >> .git/hooks/commit-msg
	@echo 'SUBJECT=$$(head -1 "$$MSG_FILE")' >> .git/hooks/commit-msg
	@echo '' >> .git/hooks/commit-msg
	@echo '# --- Conventional commit format ---' >> .git/hooks/commit-msg
	@echo 'PATTERN="^(feat|fix|refactor|test|docs|chore|ci|perf)(\(.+\))?: .{1,72}$$"' >> .git/hooks/commit-msg
	@echo 'if ! echo "$$SUBJECT" | grep -qE "$$PATTERN"; then' >> .git/hooks/commit-msg
	@echo '  echo "ERROR: commit subject must match conventional commits format:"' >> .git/hooks/commit-msg
	@echo '  echo "  <type>(<scope>): <subject>"' >> .git/hooks/commit-msg
	@echo '  echo "  Types: feat, fix, refactor, test, docs, chore, ci, perf"' >> .git/hooks/commit-msg
	@echo '  echo "  Max 72 chars. Got: $$SUBJECT"' >> .git/hooks/commit-msg
	@echo '  exit 1' >> .git/hooks/commit-msg
	@echo 'fi' >> .git/hooks/commit-msg
	@echo '' >> .git/hooks/commit-msg
	@echo '# --- Subject line: no period at end ---' >> .git/hooks/commit-msg
	@echo 'if echo "$$SUBJECT" | grep -qE "\.$$"; then' >> .git/hooks/commit-msg
	@echo '  echo "ERROR: commit subject should not end with a period"' >> .git/hooks/commit-msg
	@echo '  exit 1' >> .git/hooks/commit-msg
	@echo 'fi' >> .git/hooks/commit-msg
	@chmod +x .git/hooks/commit-msg
	@echo "Hooks installed for email: $(email)"
	@echo "  .git/hooks/pre-commit  (email gate, gofmt, go vet, go build, secrets check)"
	@echo "  .git/hooks/commit-msg  (conventional commits format)"
