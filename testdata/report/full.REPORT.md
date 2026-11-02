---
title: "Discover report: shop"
type: briefing
related order: none
version: "0.4.0"
last_reviewed: "2026-10-30T09:00:04Z"
---

# Discover report: shop

## Repo metadata (attestation echo)

| Field | Value |
|---|---|
| Tool | discover `0.4.0`, commit `9f8e7d6` |
| Scan | started `2026-10-30T09:00:00Z`, finished `2026-10-30T09:00:04Z` |
| Root | `shop` |
| Mode | git |
| Branch | `main` |
| Head | `4c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d` |
| Working tree | dirty, 2 path(s): `README.md`, `web/src/app.ts` |
| Tree | 42 files, 183204 bytes, 1 links, 0 submodules, 0 unreadable |
| Tree digest | `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa` |
| Untracked | 2 files, 512 bytes, 0 links, 0 submodules, 0 unreadable; digest `bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb` |
| Read-only proof | EQUAL: before `cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc`, after `cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc` |
| Read-only proof, whole scan | every repository of the scan EQUAL |
| Read manifest | 1 files, 120 bytes read |

## 1. Top-level inventory

42 files, 183204 bytes in total.

**Top level**

| Name | Kind |
|---|---|
| `Makefile` | file |
| `cmd` | dir |
| `current` | symlink |
| `go.mod` | file |
| `web` | dir |

**Languages**

| Language | Files |
|---|---|
| Go | 20 |
| TypeScript | 12 |

## 2. Tech stack inventory

| Layer | Technology | Version | Source file:key |
|---|---|---|---|
| backend | Go | `1.22` | `go.mod:3` |
| build | Node.js | not pinned | `web/package.json:engines` |
| frontend | React | `18.3.1` | `web/package.json:dependencies.react` |

## 3. Project structure deep-dive

**Patterns**

| Observation | Examples |
|---|---|
| Handlers are grouped by resource, one file each | `internal/http/orders.go:12` |

**Naming**

| Observation | Examples |
|---|---|
| Test files sit beside the code they test | `internal/http/orders_test.go:1` |

**Architecture**

| Observation | Examples |
|---|---|
| The web client reaches the API only through one fetch wrapper | `web/src/api.ts:8`, `web/src/app.ts:30` |

**Narrative, written by model example-model**

| Claim | Citations |
|---|---|
| The service is one Go binary with a separate web client | `cmd/shop/main.go:10`, `web/package.json:name` |

## 4. Entry points

| Kind | Name | Source |
|---|---|---|
| main | `shop` | `cmd/shop/main.go:10` |
| script | `dev` | `web/package.json:scripts.dev` |

## 5. Build / run / test

Commands are cited from the file that defines them; none was executed.

**Commands**

| Kind | Command | Source |
|---|---|---|
| build | `go build ./cmd/shop` | `Makefile:4` |
| run | `npm run dev` | `web/package.json:scripts.dev` |
| test | `go test ./...` | `Makefile:7` |
| lint | `npm run lint` | `web/package.json:scripts.lint` |

**CI**

| System | Source |
|---|---|
| GitHub Actions | `.github/workflows/ci.yml:1` |

**Containers**

| Kind | Image | Source |
|---|---|---|
| dockerfile | `golang:1.22-alpine` | `Dockerfile:1` |
| compose | none named | `compose.yaml:services.db` |

## 6. Cross-repo wiring

| Target | Relation | Source |
|---|---|---|
| `billing-api` | calls | `internal/billing/client.go:22` |
| `postgres` | depends on | `compose.yaml:services.db.image` |

## 7. Findings

**Hot spots**

| Severity | Finding | Source |
|---|---|---|
| medium | The order handler is the largest file in the repository | `internal/http/orders.go:40` |

**Anti-patterns**

| Severity | Finding | Source |
|---|---|---|
| low | An error is caught and ignored | `web/src/app.ts:55` |

**Secrets by location**

| Severity | Type | Location |
|---|---|---|
| high | `aws-access-key-id` | `config/dev.env:3` |

**Dead-code candidates**

| Severity | Finding | Source |
|---|---|---|
| info | No caller of this package was found | `internal/legacy/export.go:1` |

**TODO / FIXME (1, at most 30 are kept)**

| Severity | Finding | Source |
|---|---|---|
| info | TODO\: retry the payment call | `internal/http/orders.go:88` |

**Prompt-injection findings**

| Severity | Finding | Source |
|---|---|---|
| medium | A comment addresses an automated reader and asks it to skip the review | `docs/notes.md:14` |

## 8. UI strings & screens preview

Inventory only: strings and screens are listed where they are defined, never rendered or judged.

| Kind | Text | Source |
|---|---|---|
| screen | Checkout | `web/src/pages/Checkout.tsx:1` |
| string | Your cart is empty | `web/src/i18n/en.json:cart.empty` |

## 9. Open questions

| Topic | Question | Context | Options |
|---|---|---|---|
| Billing | Is billing-api owned by this team? | `internal/billing/client.go:22` | (1) Owned here; (2) Owned by another team; (3) A third-party service |

## 10. Recommendations

| Recommendation | Citations |
|---|---|
| Feed the stack table into the technology register | `go.mod:3` |
