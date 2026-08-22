<!-- AGENTS.md — authoritative guidance for AI coding agents working in this repo. -->
<!-- Claude Code reads CLAUDE.md; that file is a thin shim that points here. Keep AGENTS.md as the single source of truth. -->

# AGENTS.md — terraform-provider-zimaos

Guidance for AI coding agents (Claude Code, OpenCode, Codex, etc.) contributing to this
Terraform provider for **ZimaOS**. This file is the canonical agent-facing doc. `CLAUDE.md`
at repo root is a symlink/trampoline to this file — do not duplicate guidance there.

## 1. What this project is

A Terraform provider (Go, `terraform-plugin-framework`) that manages a single ZimaOS device
declaratively: install/update/remove Docker Compose apps, manage disk/RAID, users & Samba
shares, and device-wide singleton settings. The durable product/design context lives in the
Obsidian vault `ZimaOS-Terraform-Provider-Vault` (PRDs 001–004, `02-Architecture/Architecture.md`,
and research logs under `03-Research/`). Read those before making design calls.

**Grounding status (2026-08-21):**
- PRD-001 **App Management** — spec-confirmed. Real OpenAPI: `CasaOS-AppManagement/api/app_management/openapi.yaml`.
- PRD-002/003/004 (Local Storage, Users, System) — **ASSUMED**. Their real specs are gated in the
  private `IceWhale-OpenAPI` repo. Do NOT invent endpoints for these; gate the code behind a clearly
  marked "spec not yet fetched" state or ask the user.

## 2. Repository layout

```
terraform-provider-zimaos/
├── AGENTS.md                 # this file (source of truth for agents)
├── CLAUDE.md                 # shim → AGENTS.md (don't edit guidance here)
├── provider.go              # Provider, SchemaVersion, Configure()
├── internal/
│   ├── client/              # thin HTTP client per API surface (appmgmt, storage, users, system)
│   │   └── client.go        # auth (Authorization: <token>), base URL, shared Do()
│   └── provider/            # resource + datasource model types (one file per resource)
├── zimaos/                  # generated provider registration (tfplugingen)
├── examples/                # terraform config examples per resource
├── go.mod / go.sum
└── Makefile                 # build, test, install
```

## 3. Hard rules for agents

1. **Framework, not SDKv2.** Use `terraform-plugin-framework`. Resources implement
   `resource.Resource` + `resource.ResourceWithImportState` where import applies.
2. **One resource per file** under `internal/provider/`. Name: `resource_app.go`, etc.
3. **No oapi-codegen for resources.** OpenAPI may generate the *client* layer, but resource
   lifecycle (ForceNew, computed drift, import, plan-time validation) is always hand-written.
4. **Import is primary onboarding**, not an add-on. Ship `ImportState()` in the same PR as `Create`.
5. **Never default to silent data loss.** `zimaos_app.retain_config_on_destroy` defaults `false`
   (inverse of API `delete_config_folder` default `true`). RAID delete needs a `force_delete` guard.
6. **Sensitive fields.** `zimaos_user.password` is `Sensitive: true`, `Optional+Computed`, write-only.
7. **Dry-run on plan** for apps: call `POST /compose?dry_run=true&check_port_conflict=true` in the
   plan phase (confirmed param).
8. **Singletons** (`zimaos_remote_access`): `Create`/`Update` collapse to one `PUT`; `Delete` sets
   `enabled=false`. Import ID literal `singleton`. Warn if `Create` is invoked without import.

## 4. Code conventions

- **Provider schema:** `provider.go` `Configure()` reads `host` (url) and `token` (string, `Sensitive`)
  from the provider block, builds `*client.Client`, stores it in `providerData`.
- **Client:** `internal/client/client.go` holds `Client{ BaseURL, Token, HTTPClient }` and a `Do(ctx, method, path, query, body, out)` helper that injects `Authorization: <token>` and decodes JSON/YAML.
- **Resources:** each implements `Metadata`, `Schema`, `Create`, `Read`, `Update`, `Delete`, and
  `ImportState` where applicable. Computed attributes not returned by the API are marked `Computed`.
- **Naming:** Go types `appResource`, `appResourceModel`; TF resource name `zimaos_app`.
- **Tests:** acceptance tests use `dockur/zima` container; run behind `TF_ACC=1`. Tag with
  `resource.Test` + `resource.TestStep`. Import acceptance tests ship alongside Create.

## 5. Build & verify (run these; never claim "works" without them)

```bash
go build ./...            # must compile
go vet ./...              # lint-level check
go test ./...             # unit tests (acceptance gated by TF_ACC=1)
TF_ACC=1 go test ./...    # acceptance tests against dockur/zima (needs container)
```

- A green `go build ./...` + `go vet ./...` is the **minimum** bar before reporting a task done.
- For resources whose spec is gated (002/003/004), do not write `Create`/`Update`/`Delete` bodies
  against guessed endpoints. Implement the schema + a clearly-failing/guarded client method, and leave
  a `// SPEC GATED: <repo/path> not yet fetched — see AGENTS.md §1` comment.

## 6. When a PRD's spec is gated (current state for 002/003/004)

The real OpenAPI for Local Storage / Users / System lives in the private `IceWhale-OpenAPI` repo
(404 as of 2026-08-21). Until those YAMLs are available:
1. Do not implement client calls for those surfaces.
2. If asked to scaffold, build the resource *schema* from the PRD's attribute table, but mark every
   API-bound method `// SPEC GATED`.
3. Surface the gap to the user rather than inventing `POST /v2/...` paths.

## 7. Linked context (read before design decisions)

- Vault `Home.md` → `ZimaOS-Terraform-Provider-Vault/Home.md`
- `02-Architecture/Architecture.md` (resource taxonomy, D1–D6 decisions)
- `03-Research/Research-Log 2026-08-21 ZimaOS API Spec Discovery.md` (spec source map + confirmations)
- PRDs 001–004 under `01-PRDs/`

