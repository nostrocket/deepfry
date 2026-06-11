---
status: complete
phase: 05-pubkey-validation-hardening
source: [05-VERIFICATION.md]
started: 2026-06-10T13:00:00Z
updated: 2026-06-11T12:32:00Z
---

## Current Test

[testing complete]

## Tests

### 1. TestMarkAttemptedRecoverOrPurge — Live Dgraph Integration

expected: |
  Run from `web-of-trust/`:
  `go test -tags=integration ./pkg/dgraph/ -run TestMarkAttemptedRecoverOrPurge -v`
  
  Expected: Test passes; log shows uppercase node recovered to lowercase
  (last_attempt=0 confirmed by queryLastAttempt), short-hex garbage node deleted,
  relay-blob garbage node deleted; cleanup of recovered node succeeds
result: pass

### 2. TestWritePathRejectsGarbage — Live Dgraph Integration

expected: |
  Run from `web-of-trust/`:
  `go test -tags=integration ./pkg/dgraph/ -run TestWritePathRejectsGarbage -v`
  
  Expected: Test passes; uppercase hex, short hex, and relay-blob strings resolve to
  zero nodes after AddFollowers call; valid followee was written; cleanup succeeds
result: pass

## Summary

total: 2
passed: 2
issues: 0
pending: 0
skipped: 0
blocked: 0

## Gaps
