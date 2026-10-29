# The discover report schema, v1

`discover scan` writes one JSON document. Its contract is `schema/discover-v1.schema.json` (JSON Schema 2020-12,
`$id` `https://github.com/saphan-ai/discover/schema/discover-v1.schema.json`), the writer's strict form;
`schema/discover-v1.reader.schema.json` is the tolerant form a reader uses (see the compatibility rule). The Go types
in `internal/model` match the writer schema field for field, and a test fails if they drift.

## Rules that hold everywhere

- **Every claim cites its source.** A `source` is `{file, key}` or `{file, line}` (at least one of `key` and `line`).
  `file` is relative to the repository root; an absolute path is refused.
- **A path is plain relative.** Every path field (`source.file`, `read_manifest.entries[].path`, `git.dirty_paths[]`,
  `workspace_profile.repos[].relative_path` and `convention_files[]`) refuses a leading `/` or `\`, a drive letter
  (`C:`), any `..` segment and an empty segment. `.` alone is the root.
- **A secret is a location and a type.** A finding of kind `secret` carries `secret_type` and no `text`. No field can
  hold the value, because a finding accepts no field the schema does not name, for a writer or for a reader (see
  the compatibility rule), and the two free fields it keeps are bounded: `secret_type` is a slug
  (`^[a-z0-9][a-z0-9-]{0,63}$`, for example `aws-access-key-id`), and `source.key` inside a secret finding is a key
  path (`^[A-Za-z_][A-Za-z0-9_.\-\[\]]{0,127}$`, for example `services.api.env[0].API_TOKEN`), never a value. Outside
  a secret finding `source.key` is unchanged. A slug or a key path can still be a poorly chosen word; the schema
  stops a value that has an `=`, a space or a quote in it, and cannot know what a writer meant by the rest.
- **A member name is unique and exact.** A document holding the same member name twice in one object, or a name that
  differs from a known field only in case (`Findings` beside `findings`), is refused, by a writer and by a reader.
  Readers in other languages must do the same: RFC 8259 §4 says names SHOULD be unique, and parsers disagree on
  which copy wins, so a duplicate can show one reader a different document from the one that was checked. JSON Schema
  cannot express either rule, so the schema files do not carry it; `model.Validate` and `model.ValidateForRead` do.
- **A command is cited, never run.** `commands[].executed` is always `false`.
- **An empty section is not deleted.** Its array stays and `absent[]` carries one line: the section number (1 to 10)
  and the reason.
- **No synthesis across repositories.** `repos[]` is an array even for one path; each entry stands alone.

## Top level

| Field | Required | Meaning |
|---|---|---|
| `schema_version` | yes | Always `"discover/v1"`. |
| `tool` | yes | `name`, `version`, `commit` of the program that wrote the report. |
| `scan` | yes | `started`, `finished` (RFC 3339), `targets[]` (`path`, `mode` `git` or `dir`), `read_only_proof_all_equal` (boolean). The per-repository proofs are the record; this is only their summary. |
| `repos` | yes | One report per repository, at least one. |
| `workspace_profile` | yes | What Studio Solo imports at setup. See below. |
| `extensions` | no | An object for later editions. Any content. |

## One repository (`repos[]`)

The ten sections of the per-repo report map to fields as follows.

| Section | Field | Row |
|---|---|---|
| metadata | `name`, `attestation` | `name` is the base name. `attestation` is described below. |
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

### The attestation and the read-only proof

`repos[].attestation` is what the scan knew of the repository, the proof that it left the repository unchanged, and
what it read. Each repository has its own; a scan of several targets writes several, and the proof is never shared.

| Field | Required | Meaning |
|---|---|---|
| `root` | yes | The base name of the scanned directory; its absolute path only when the user passed `--record-root`. |
| `mode` | yes | `git` (a git working tree) or `dir` (a plain directory). |
| `git` | iff `mode` is `git` | `branch`, `detached`, `head`, `clean`, `dirty_paths[]` (relative paths). `branch` and `head` are empty strings when there is none. Refused when `mode` is `dir`. |
| `tree` | yes | `files`, `bytes`, `links`, `submodules`, `unreadable` (counts) and `digest` (64 lowercase hex). |
| `untracked` | no | A second `tree`, for the untracked files of a git working tree. Refused when `mode` is `dir`. |
| `read_only_proof` | yes | `status` (`EQUAL` or `NOT-EQUAL`), `before` and `after` (64 lowercase hex): the tree digest before and after the scan. |
| `read_manifest` | yes | Every file read: `entries[]` of `path`, `sha256`, `bytes`; `files_read`; `bytes_read`. |

`status` is the verdict the scan reached by comparing `before` and `after`; the schema checks the form of the three
fields, not that the verdict follows from the two digests.

## The workspace profile

`workspace_profile.repos[]` is the part Studio Solo imports. Each entry has `name`, `relative_path` (relative to the
scan root, `.` for the root itself), `vcs` (`git` or `none`), `languages[]`, `commands` (`build[]`, `test[]`, `run[]`,
each `text` and `source`), `convention_files[]` (paths of `AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING.md`, `.editorconfig`
and CI files that were found) and `ci[]`.

`testdata/schema/profile-v1.json` is a complete profile on its own, as a bare `workspace_profile` object. Copy it as a
fixture.

## Compatibility rule

One rule, two validators.

- **Version 1 only grows optional fields.** Removing, renaming or retyping a field, or adding any field to a
  `finding`, is **version 2**, and the `schema_version` value changes with it.
- **A writer is strict.** Every object is closed: what `discover` writes carries no field this document does not
  name. `schema/discover-v1.schema.json` and `model.Validate` enforce that, and `Marshal` runs `Validate` before a
  report is written.
- **A reader is tolerant.** A v1 reader ignores an unknown field everywhere **except inside a `finding`** (and the
  `source` inside it), which stays closed for readers too. A newer v1 file that carries one more optional field on
  a repository, in `workspace_profile` or in `extensions` is therefore read; a file that carries a new field on a
  finding is refused, naming the field. The reason is the secret rule: no later file can carry a secret's value in a
  field an older reader would pass through. `schema/discover-v1.reader.schema.json` and `model.ValidateForRead`
  enforce that. Every other rule (required fields, types, enums, patterns, the `executed` constant, the cross-field
  rules, and the duplicate-name and case rule above) holds for a reader unchanged. `model.ValidateForRead` builds the
  value it returns from the tree it checked, never from a second reading of the bytes.
- **A reader refuses any `schema_version` it does not know.**

The reader schema is generated from the writer schema by a test helper in `internal/model` and is never edited by
hand; a test fails if the two drift. After a change to the writer schema, regenerate it with:

```
go test -count=1 -run TestReaderSchemaIsGenerated ./internal/model/ -args -update-reader-schema
```

## Checking a document

`model.Validate` (package `internal/model`) is the writer's check: it refuses a document that breaks any rule above
and names the field by its JSON pointer, for example `/repos/0/commands/1/executed`. The renderer wagon (W8) is
the order that wires it in: until then no CLI code calls it, and `Marshal` is the way to write a checked report. `model.ValidateForRead` is the reader's check, for a document another v1 writer produced. The schema
files are the contract for other languages; `testdata/schema/valid/` and `testdata/schema/invalid/` are the
examples, and every invalid file differs from a valid one in exactly one field (`invalid/EXPECT.json` names it).
`testdata/schema/forward/` holds the two files a later v1 writer might produce: one with an unknown optional field
(a reader accepts it, a writer refuses it) and one with an unknown field on a finding (both refuse it).

## Not run

The schema test uses a JSON Schema library that is a test-only dependency; the binary does not link it.
