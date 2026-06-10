---
status: testing
phase: 05-pubkey-validation-hardening
source: [05-VERIFICATION.md]
started: 2026-06-10T13:00:00Z
updated: 2026-06-10T13:00:00Z
---

## Current Test

number: 1
name: TestMarkAttemptedRecoverOrPurge — Live Dgraph Integration
expected: |
  TestMarkAttemptedRecoverOrPurge passes: uppercase node recovered to lowercase
  (last_attempt unset), short-hex node deleted, relay-blob node deleted
awaiting: user response

## Tests

### 1. TestMarkAttemptedRecoverOrPurge — Live Dgraph Integration

expected: |
  Run from `web-of-trust/`:
  `go test -tags=integration ./pkg/dgraph/ -run TestMarkAttemptedRecoverOrPurge -v`
  
  Expected: Test passes; log shows uppercase node recovered to lowercase
  (last_attempt=0 confirmed by queryLastAttempt), short-hex garbage node deleted,
  relay-blob garbage node deleted; cleanup of recovered node succeeds
result: [pending]

### 2. TestWritePathRejectsGarbage — Live Dgraph Integration

expected: |
  Run from `web-of-trust/`:
  `go test -tags=integration ./pkg/dgraph/ -run TestWritePathRejectsGarbage -v`
  
  Expected: Test passes; uppercase hex, short hex, and relay-blob strings resolve to
  zero nodes after AddFollowers call; valid followee was written; cleanup succeeds
result: [pending]

## Summary

total: 2
passed: 0
issues: 0
pending: 2
skipped: 0
blocked: 0

## Gaps
