# Root Layout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ensure the repo layout follows Go best practice for a single-binary app and document constraints that justify keeping the entry point at repo root, config.yaml embed, and package main — no `cmd/` move needed.

**Architecture:** Single-binary app with `main` package at repo root; config.yaml embedded via `//go:embed` at compile time; all `.go` files that are `package main` must co-reside in the same directory as the binary (root). The `cmd/` convention from the community layout guide is for repos with multiple binaries or library+binaries combos; this repo's single binary + embed constraint naturally keeps everything at root.

**Tech Stack:** Go, `tinygo.org/x/bluetooth`, `bubbletea`, standard library, `//go:embed` for config.yaml.

**Spec:** The two user tasks from _todo.md: (1) are go files at root in good location?, (2) cleanup CLI options. This plan addresses Task 1 only.

**Global Constraints:**
- go test ./... must pass (111 passed / 6 pkgs).
- go vet ./... clean.
- gofmt -l . currently flags every file (pre-existing CRLF noise); edited files must remain content-gofmt-clean.
- GOOS=windows go build ./... must stay clean (required for tap_windows.go vet).
- The `//go:embed config.yaml` in `defaultconfig.go` (line 15) requires `config.yaml` to remain in the same directory as `defaultconfig.go` — i.e., at repo root.
- `package main` can only live at root (or in a `cmd/` directory); moving support files would require restructure of all callers and is out of scope.

--- 

### Task 1: Audit root .go files and document rationale

**Files to inspect:**
- `main.go` — entry point; imports `ble`, `config`, `keys`, `tui`, `zwift`; all `package main`.
- `tuirun.go` — `package main` TUI wiring; references `tea`, `ble`, `config`, `keys`, `tui`.
- `defaultconfig.go` — `package main`; line 15: `//go:embed config.yaml`; also `legacyConfigPaths` var and migration logic.

**Verification steps:**
1. Run `go vet ./...` — should be clean (currently clean).
2. Run `go run .` — should start (no hardware; stubs mode).
3. Run `gofmt -l .` — currently flags every file (pre-existing CRLF noise). Verify no *new* warnings introduced by our changes (we make no edits to .go content beyond possible prose fix in main.go doc comment, but task is layout doc only).
4. Run `GOOS=windows go build ./...` — should pass vet of tap_windows.go (currently clean; unchanged).

**Conclusion:**
The current layout is consistent with Go best practice for a single-binary app. The `cmd/` convention is intended for repos with multiple binaries or library+binaries combos. This repo's single binary + `//go:embed config.yaml` constraint means moving to `cmd/` would require relocating `config.yaml` and updating the embed path — a larger refactor with no benefit. Plan recommends strengthening the AGENTS.md Layout section to explicitly mention these constraints.

### Task 2: Document rationale in AGENTS.md

**Files to modify:**
- `AGENTS.md` — Layout section. Add a concise sentence explaining why the single binary lives at repo root and the embed constraint. No code changes needed beyond this doc update.

**Exact change:**
In `AGENTS.md` under the `## Layout` heading, after the existing bullet about "a single-binary app, so the entry point lives at the repo root (not cmd/)", insert:
> The repo root layout is a Go best practice for single-binaries; the `//go:embed config.yaml` directive in `defaultconfig.go` requires `config.yaml` to stay in the same directory as the source file, and all `package main` files must remain at root. Moving to `cmd/` would require a larger refactor (embed path change, build script updates) and is out of scope.

**Verification:**
- Run `go test ./...` (green).
- Run `go vet ./...`.
- Run `gofmt -l .` — expect only pre-existing CRLF noise.
- Ensure `GOOS=windows go build ./...` still passes.

**Commit:**
`docs: document single-binary-at-root layout and its constraints`