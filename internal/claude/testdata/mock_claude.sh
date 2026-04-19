#!/bin/bash
# Mock Claude Code subprocess for testing.
# Supports --exit-immediately, --ignore-sigint, --echo, and --slow-exit flags.

exit_immediately=false
ignore_sigint=false
echo_mode=false
slow_exit=false

for arg in "$@"; do
  case "$arg" in
    --exit-immediately)
      exit_immediately=true
      ;;
    --ignore-sigint)
      ignore_sigint=true
      ;;
    --echo)
      echo_mode=true
      ;;
    --slow-exit)
      slow_exit=true
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
elif "$slow_exit"; then
  echo '{"type":"system","subtype":"init","session_id":"test-session-123"}'
  sleep 2
  exit 1
elif "$echo_mode"; then
  echo '{"type":"system","subtype":"init","session_id":"test-session-123"}'
  trap cleanup SIGINT
  while read -r line; do
    # Echo back as assistant event
    echo '{"type":"assistant","subtype":"text","message":{"content":[{"type":"text","text":"echo: '$line'"}]}}'
  done
else
  echo '{"type":"system","subtype":"init","session_id":"test-session-123"}'
  trap cleanup SIGINT
  # Wait for input or signal
  while :; do
    sleep 1
  done
fi
