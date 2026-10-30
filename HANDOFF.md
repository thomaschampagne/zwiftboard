
---

## Follow-on: review pass vs `dev` (2026-10-30)

Senior Go / UX / security review of `feat/left-controller-support-zwift-sign-in`
against `dev`. Fixed:

- **Recovery restored a stale legacy config.** `recoverConfig` ran
  `ensureConfig`, which migrates `config.yaml` beside the exe first. A stale
  copy came back, failed validation, and recovery aborted. Recovery now calls
  `writeDefault` (no migration). Test: `TestRecoverConfigIgnoresLegacyConfig`.
- **Sign-in blocked BLE.** `Client.Login` held the client mutex across the
  Zwift HTTP call (up to 20s), stalling `Ready()`/`Solve`. The request now runs
  outside the lock; only the result is published under it. Test:
  `TestLoginDoesNotBlockReadyWhileWaitingForZwift`. `Solve`/refresh keep the lock
  on purpose: concurrent Keycloak refreshes with a rotating refresh token could
  kill the session.
- **Dead code.** Removed `Client.Ensure` and `Store.Delete` (no production
  callers). Their refresh tests now drive `Solve`.
- **Lint.** Fixed the 13 new golangci-lint findings in branch-touched code
  (unchecked `Close`/`Remove`/`Write`, capitalised error string, ineffassign,
  staticcheck). The 12 remaining findings already exist on `dev`.
- **Caret drift.** The email field rendered trimmed while the caret counted
  untrimmed runes; it now renders exactly as typed.

Open (not changed, needs a decision):

1. **Debug logs contain the server reply.** `dlock response` logs the full
   23-byte body in hex (and the challenge). AGENTS.md allows this for
   diagnosis, but the reply may be a usable token (see the 2026-10-26 notes).
   Decide: keep at Debug only, or log length + a short hash.
2. **Lifetime copy conflict.** `login.go`, `tuirun.go` and their tests say the
   unlock "lasts a few days"; AGENTS.md says "~24h". Confirm the real lifetime
   (Experiment A) before changing either.
3. **Replay experiments in this file** (Experiment C) describe a bypass; keep
   them research-only, as already stated above.

Verification: `just ci` green; `go test -race -count=1 ./...` green;
`golangci-lint run` 12 issues, all pre-existing on `dev`.
