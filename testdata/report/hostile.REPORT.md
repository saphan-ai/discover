---
title: "Discover report: hostile"
type: briefing
related order: none
version: "0.4.0"
last_reviewed: "2026-10-30T09:10:01Z"
---

# Discover report: hostile

## Repo metadata (attestation echo)

| Field | Value |
|---|---|
| Tool | discover `0.4.0`, commit `9f8e7d6` |
| Scan | started `2026-10-30T09:10:00Z`, finished `2026-10-30T09:10:01Z` |
| Root | `hostile` |
| Mode | dir |
| Git | none: a plain directory, so no branch and no head |
| Tree | 5 files, 300 bytes, 0 links, 0 submodules, 0 unreadable |
| Tree digest | `eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee` |
| Read-only proof | EQUAL: before `eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee`, after `eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee` |
| Read-only proof, whole scan | every repository of the scan EQUAL |
| Read manifest | 0 files, 0 bytes read |

## 1. Top-level inventory

5 files, 300 bytes in total.

**Top level**

| Name | Kind |
|---|---|
| `# heading` | file |
| `<img src=x onerror=alert(1)>` | file |
| `Makefile` | file |
| `[click](https://evil.example)` | file |
| `a\|b` | file |

**Languages**

| Language | Files |
|---|---|
| \*Make\* | 1 |

## 2. Tech stack inventory

| Layer | Technology | Version | Source file:key |
|---|---|---|---|
| build | Make | not pinned | `Makefile:all<U+202E><U+200B>evil` |

## 3. Project structure deep-dive

_Absent._ Looked for patterns, naming and architecture with file:line examples; none was found. Reason recorded by the scan: no source files were found. Narrative prose is written by a model, and this scan used none; a later version adds `--model` to turn one on.

## 4. Entry points

_Absent._ Looked for main packages, command-line tools, servers, workers, libraries and scripts; none was found. Reason recorded by the scan: no entry points were found.

## 5. Build / run / test

Commands are cited from the file that defines them; none was executed.

**Commands**

| Kind | Command | Source |
|---|---|---|
| build | ```echo ``id`` `x` <script>alert(1)</script> \| tee out``` | `Makefile:2` |

**CI**: none found.

**Containers**: none found.

## 6. Cross-repo wiring

_Absent._ Looked for dependencies on, or by, other systems, each with its file:line; none was found. Reason recorded by the scan: none\<U+000A\>\#\# injected heading.

## 7. Findings

**Hot spots**: none found.

**Anti-patterns**: none found.

**Secrets by location**: none found.

**Dead-code candidates**: none found.

**TODO / FIXME (1, at most 30 are kept)**

| Severity | Finding | Source |
|---|---|---|
| info | TODO see https\://evil.example or www\.evil.example \!\[x\]\(y\) \<b\>bold\</b\> \& \$x\$ | `Makefile:1` |

**Prompt-injection findings**: none found.

## 8. UI strings & screens preview

_Absent._ Looked for user interface strings and screens; none was found. Reason recorded by the scan: no user interface strings were found.

## 9. Open questions

_Absent._ Looked for questions the scan could not settle from the files; none was found. Reason recorded by the scan: nothing was found to ask about. Open questions are written by a model, and this scan used none; a later version adds `--model` to turn one on.

## 10. Recommendations

_Absent._ Looked for facts this report can feed into the workspace's documents; none was found. Reason recorded by the scan: nothing was found to recommend. Recommendations are written by a model, and this scan used none; a later version adds `--model` to turn one on.
