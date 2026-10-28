# The discover report schema, v1

`discover scan` writes one JSON document. Its contract is `schema/discover-v1.schema.json` (JSON Schema 2020-12,
`$id` `https://github.com/saphan-ai/discover/schema/discover-v1.schema.json`). The Go types in `internal/model` match
it field for field, and a test fails if they drift.

## Rules that hold everywhere

- **Every claim cites its source.** A `source` is `{file, key}` or `{file, line}` (at least one of `key` and `line`).
  `file` is relative to the repository root; an absolute path is refused.
- **A secret is a location and a type.** A finding of kind `secret` carries `secret_type` and no `text`. No field can
  hold the value, because no object accepts a field the schema does not name.
- **A command is cited, never run.** `commands[].executed` is always `false`.
- **An empty section is not deleted.** Its array stays and `absent[]` carries one line: the section number (1 to 10)
  and the reason.
- **No synthesis across repositories.** `repos[]` is an array even for one path; each entry stands alone.

## Top level

| Field | Required | Meaning |
|---|---|---|
| `schema_version` | yes | Always `"discover/v1"`. |
| `tool` | yes | `name`, `version`, `commit` of the program that wrote the report. |
| `scan` | yes | `started`, `finished` (RFC 3339), `targets[]` (`path`, `mode` `repository` or `workspace`), `readonly_proof` (`before`, `after`, `equal`). |
| `repos` | yes | One report per repository, at least one. |
| `workspace_profile` | yes | What Studio Solo imports at setup. See below. |
| `extensions` | no | An object for later editions. Any content. v1 readers ignore it. |

## One repository (`repos[]`)

The ten sections of the per-repo report map to fields as follows.

| Section | Field | Row |
|---|---|---|
| metadata | `name`, `attestation` | `name` is the base name. `attestation` has `head`, `branch` (null when unknown), `dirty` and `read_manifest` (`entries[]` of `path`, `sha256`, `bytes`; `files_read`; `bytes_read`). |
| 1 Inventory | `inventory` | `top_level[]` (`name`, `kind`), `languages[]` (`name`, `files`), `files_total`, `bytes_total`. |
| 2 Tech stack | `stack[]` | `layer`, `technology`, `version`, `source`. `version` is a string, or `null` with `version_note` `"not pinned"`. |
| 3 Project structure | `structure` | `patterns[]`, `naming[]`, `architecture[]`; each is `text` and `examples[]` (sources, at least one). |
| 4 Entry points | `entry_points[]` | `kind`, `name`, `source`. |
| 5 Build, run, test | `commands[]`, `ci[]`, `containers[]` | `commands`: `kind` (`build`, `run`, `test`, `lint`), `text`, `source`, `executed`. `ci`: `system`, `source`. `containers`: `kind`, optional `image`, `source`. |
| 6 Cross-repo wiring | `wiring[]` | `target`, `relation`, `source`. |
| 7 Findings | `findings[]` | `kind` (`hot_spot`, `anti_pattern`, `secret`, `dead_code_candidate`, `todo`, `prompt_injection`), `severity` (`info`, `low`, `medium`, `high`), `source`, optional `text`, and `secret_type` for secrets only. At most 30 findings of kind `todo` per repository. |
| 8 UI strings and screens | `ui[]` | `kind` (`string`, `screen`), `text`, `source`. Inventory only. |
| (model output) | `narrative` | `claims[]`, each `text` and `citations[]` (at least one source). Empty when no model was used. Optional `model`. |
| 9 Open questions | `open_questions[]` | `topic`, `question`, `context` (a source), `options[]` (two or three). |
| 10 Recommendations | `recommendations[]` | `text`, `citations[]` (at least one source). |
| honest absence | `absent[]` | `section` (1 to 10), `reason`. |

## The workspace profile

`workspace_profile.repos[]` is the part Studio Solo imports. Each entry has `name`, `relative_path` (relative to the
scan root, `.` for the root itself), `vcs` (`git` or `none`), `languages[]`, `commands` (`build[]`, `test[]`, `run[]`,
each `text` and `source`), `convention_files[]` (paths of `AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING.md`, `.editorconfig`
and CI files that were found) and `ci[]`.

`testdata/schema/profile-v1.json` is a complete profile on its own, as a bare `workspace_profile` object. Copy it as a
fixture.

## Compatibility rule

- Version 1 only grows **optional** fields. A field that is removed, renamed or retyped is **version 2**, and the
  `schema_version` value changes with it.
- A reader ignores unknown fields **only inside `extensions`**. Everywhere else an unknown field is refused, so a
  reader that does not know a newer optional field refuses the document rather than guessing. A new optional field
  therefore ships with a release of the readers that know it.
- A reader refuses any `schema_version` it does not know.

## Checking a document

`model.Validate` (package `internal/model`) refuses a document that breaks any rule above and names the field by its
JSON pointer, for example `/repos/0/commands/1/executed`. The CLI calls it before it writes a report. The schema file
is the contract for other languages; `testdata/schema/valid/` and `testdata/schema/invalid/` are the examples, and
every invalid file differs from a valid one in exactly one field (`invalid/EXPECT.json` names it).

## Not run

The schema test uses a JSON Schema library that is a test-only dependency; the binary does not link it.
