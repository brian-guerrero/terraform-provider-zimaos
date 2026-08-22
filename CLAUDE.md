# CLAUDE.md

> **This file is a shim.** The authoritative guidance for AI coding agents lives in
> **[`AGENTS.md`](./AGENTS.md)** at this repo root. Read that file in full before making
> design or implementation decisions. Do not duplicate or fork guidance here — edit
> `AGENTS.md` instead.

## Quick pointer

All project conventions, hard rules, build/verify commands, and the spec-gating workflow
for the ZimaOS Terraform provider are documented in [`AGENTS.md`](./AGENTS.md):

- What the project is and its grounding status (which PRDs are spec-confirmed vs. gated)
- Repository layout
- Hard rules for agents (framework choice, import-primary, no silent data loss, etc.)
- Code conventions and the build/verify bar (`go build ./...`, `go vet ./...`)
- What to do when a PRD's OpenAPI spec is gated (PRDs 002/003/004)

If you are an agent and this shim and `AGENTS.md` ever disagree, **`AGENTS.md` wins.**
