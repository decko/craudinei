#!/bin/bash
# Mock Claude Code subprocess for testing.
# Supports --exit-immediately and --ignore-sigint flags.

exit_immediately=false
ignore_sigint=false

for arg in "$@"; do
  case "$arg" in
    --exit-immediately)
      exit_immediately=true
      ;;
    --ignore-sigint)
      ignore_sigint=true
      ;;
  esac
done

if "$exit_immediately"; then
  echo '{"type":"system","subtype":"init","session_id":"test-session-123"}'
  exit 0
fi

cleanup() {
  exit 0
}

if "$ignore_sigint"; then
  trap '' SIGINT
  echo '{"type":"system","subtype":"init","session_id":"test-session-123"}'
  sleep 60
else
  echo '{"type":"system","subtype":"init","session_id":"test-session-123"}'
  trap cleanup SIGINT
  # Wait for input or signal
  while :; do
    sleep 1
  done
fi