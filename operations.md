# ftask operations

An operation is a single change to, or query of, the data defined in the [design spec](design-spec.md), specified in terms of that data model. Operations are the domain layer: small and orthogonal. They are not CLI commands — the CLI may compose several operations into one command, run under a single write lock (e.g. create a task and make an existing task wait on it, in one step).

Terms follow the design spec's [Terms](design-spec.md#terms).

## Conventions

- **JSON in, JSON out.** Input and output are always JSON with published schemas, so users and agents can construct requests and parse results without scraping text. Every result is wrapped in the [output envelope](#output-envelope).
- **Input follows the file-level rules.** Input is held to the same [file-level rules](design-spec.md#file-validity) for integer literals (`2`, not `2.0` or `2e0`), duplicate keys (in the input itself or anywhere inside `extra`), unpaired surrogate escapes, and nesting depth. A violation is `invalid-input`.
- **Schema identifiers.** Shared schemas have short `$id`s (`envelope`, `error`, `warning`, `task`, `folder-path`, and the design spec's `task-file` and `root-file`). Each operation's schemas are `<op>-input`, `<op>-output`, and `<op>-partial`. Where a property has the same meaning and constraints as a task file field, the schema `$ref`s it, and its meaning is the one in [Fields](design-spec.md#fields).
- **Referring to operations and kinds.** Operation names, error kinds, and warning kinds are written in code (`create`, `conflict`, `duplicate-id`), linked on their first mention in a section. Error qualifiers are written `` `kind` (`field`: `value`) ``, e.g. `conflict` (`rule`: `id-exhausted`).
- **Parameters.** An operation takes a parameter only if it changes the meaning of the result or the work done (e.g. `frontier`'s `folder`, which limits which files are read). Narrowing or shaping output — filtering on fields, taking the first N — is left to the caller (e.g. `jq`) or the CLI.
- **Versioning.** The schemas in this document are ftask's public contract (see [Versioning](#versioning)).

## Operation kinds

Every operation is one of three kinds:

- ***read*** — Takes no lock and changes nothing. Read operations that walk the tree follow the design spec's [Walking the tree](design-spec.md#walking-the-tree) and [Reads](design-spec.md#reads) rules. Some reads (`version`, `info`) do not require a usable root.
- ***write*** — Changes the tree. Requires a usable root, takes the write lock, and follows the design spec's [Guarantees](design-spec.md#guarantees) (and [Walking the tree](design-spec.md#walking-the-tree), when it walks). Uses the general [Precedence](#precedence).
- ***setup*** — Creates what writes depend on. Only [`init`](#init) is a setup operation. It takes no lock, the write guarantees do not apply to it, and it defines its own error precedence.

## Operation template

Every operation is specified with the same parts, in this order. Every part is always present except **Order**, which appears only for operations that return a collection. An empty part is written `**Part:** none.`, optionally followed by one sentence saying why. A part may be followed by short unlabeled notes that belong to it.

| Part | Content |
|---|---|
| **Summary** | Unlabeled first paragraph: what the operation does, in one or two sentences. |
| **Kind** | One of the [operation kinds](#operation-kinds), then, in this order: whether it takes the write lock, and whether it requires a usable root. |
| **Input schema** | [JSON Schema](https://json-schema.org/) (draft 2020-12), `$id` `<op>-input`. |
| **Additional validation** | Input rules the schema can't express (e.g. per [Naming and validation](design-spec.md#naming-and-validation)). All raise `invalid-input`. |
| **Preconditions** | State that must hold beforehand. The corresponding errors are listed under Errors. A table when the outcome branches on state. |
| **Needed files** | The [needed files](#precedence), whose problems are errors (including any input path, checked by the [path walk](#path-walk)); the *relevant* files, whose problems are warnings; and how far the operation walks the tree. |
| **Effects** | The resulting state, in data-model terms — not file steps. |
| **Invariants at risk** | Which [invariants](design-spec.md#invariants) the operation could break, and how it prevents that. |
| **Output schema** | JSON Schema of `result` in the envelope on success, `$id` `<op>-output` — or a reference to a [shared schema](#shared-schemas). Structure only; rendering belongs to the CLI. |
| **Order** | Only for operations that return a collection: the order of its items, usually by reference to [Tree order](#tree-order). |
| **Errors** | Table of [error kinds](#error-kinds) and when each is raised, in [precedence](#precedence) order. Operations that require a usable root list the [Root states](#root-states) errors as one row. `io` and `internal` are omitted: any operation can raise them. |
| **Warnings** | Table of [warning kinds](#warning-kinds) and when each is reported. |
| **Partial schema** | For a write or setup that touches several files: JSON Schema of `error.partial`, `$id` `<op>-partial`, describing what took effect before it failed. |
| **Crash behavior** | What a process crash or system crash partway through can leave behind, and what [`doctor`](design-spec.md#doctor) would find. |
| **Retry safety** | Whether running it again after `busy`, an error with `partial`, or a crash is safe, and with what outcome. |

## Output envelope

Every operation returns one of two shapes:

```text
{ "ok": true,  "result": { }, "warnings": [ ] }
{ "ok": false, "error":  { }, "warnings": [ ] }
```

- **`result`** — the operation's output, per its Output schema.
- **`error`** — why the operation failed, per the [error schema](#error-schema). An error means the operation's effects did not take place, except as described by `partial`.
- **`warnings`** — problems found along the way that did not stop the operation, per the [warning schema](#warning-schema). Present, possibly empty, on success and failure alike.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "envelope",
  "oneOf": [
    {
      "type": "object",
      "required": ["ok", "result", "warnings"],
      "properties": {
        "ok": { "const": true },
        "result": { "type": "object" },
        "warnings": { "type": "array", "items": { "$ref": "warning" } }
      },
      "additionalProperties": false
    },
    {
      "type": "object",
      "required": ["ok", "error", "warnings"],
      "properties": {
        "ok": { "const": false },
        "error": { "$ref": "error" },
        "warnings": { "type": "array", "items": { "$ref": "warning" } }
      },
      "additionalProperties": false
    }
  ]
}
```

## Errors

An error means the operation failed. `kind` and `details` are the contract; `message` is for humans and may change between releases.

### Error kinds

| Kind | Meaning | `details` |
|---|---|---|
| `invalid-input` | Input failed validation. Always raised before the write lock is sought, and before any needed file is read (a check on an input path itself, like `init`'s, may inspect that path). Reports **every** invalid input, not just the first. | `problems`: list of `{field, reason}`; `field` is a JSON Pointer into the input (e.g. `/tags/2`), `reason` a human-readable string. Sorted by `field`, then by `reason` (both compared as strings, byte by byte), so the same input always yields the same list. |
| `not-initialized` | The root is *not initialized* (see [Root states](#root-states)). A file that exists but is unusable is never `not-initialized`. | `missing`: `config`, `root`, or `metadata` (meaning `ftask.json`) — the first absent piece. |
| `environment` | The process's environment lacks what ftask needs to locate its files: the home directory, from which the config location is derived (see [Config file](design-spec.md#config-file)). Not a root state — no config was looked for. | `variable`: the environment variable that is unset or unusable; currently always `HOME`. |
| `not-found` | A task or folder named by the input, or a filesystem directory it requires, does not exist. | `folders`: tree folder paths; `ids`: task IDs; `paths`: filesystem paths (e.g. `init`'s missing parent directory). All three always present, empty when not applicable. |
| `conflict` | The operation was refused because it would violate an invariant, overwrite state it must not, or act on a task the tree cannot identify uniquely. | `rule`: the rule that refused it — currently `acyclic`, `id-exhausted` (no ID left under the [ID ceiling](design-spec.md#task-ids)), `config-exists`, `root-not-empty`, `duplicate-id` (a write names an ID that more than one task file has), `id-above-last-id` (a task to remove has an ID above `last_id`), `not-empty` (a folder to delete holds tasks or folders), `destination-exists` (something is already where a folder would move). `ids`: the tasks involved, always present, possibly empty. For `acyclic`, also `cycles`: `cycles[i]` is one cycle through `ids[i]`, chosen deterministically (see [`block`](#block)). |
| `busy` | Another write holds the write lock. Safe to retry. | none (`{}`). |
| `corrupt` | A needed file — or an entry on an input path — is present and readable but its content or type is wrong (see [File validity](design-spec.md#file-validity)); or ftask found a file where, under its own invariants, none can exist (e.g. creating a task file that already exists). | `path`; `reason`: `not-json` (not parseable, or not an object), `invalid` (fails a file-level rule, including a missing `schema` or one not written as an integer literal within ±(2^53 − 1)), or `unexpected-file` (wrong entry type, e.g. `ftask.json` is a symlink or directory; or a file exists that must not). |
| `io` | The environment refused an operation: an unreadable file, permission denied, disk full, read-only filesystem, and similar. An OS error with no symbolic name is `internal`, not `io`. | `path`: built from the root as stored (see [Root path](design-spec.md#root-path)), or the config's own path for an error on the config; `code`: the symbolic OS error, e.g. `ENOSPC`, never a number. |
| `unsupported-format` | A needed file's `schema` is not the version this binary supports (see [Format versions](design-spec.md#format-versions)). | `path`; `found`: the file's version; `supported`: the versions this binary supports. |
| `internal` | A bug ftask detects. Every failure ftask reports has a kind: anything not covered above is `internal`. A crash reports nothing at all (see the CLI's [exit codes](cli-spec.md#exit-codes)). | none (`{}`). |

The `reason` fields of `invalid-input` problems, of `corrupt`, and of the `unusable-file` warning are independent: each has its own values.

### Precedence

An operation's **needed files** are the files it must read to do its job: the config and `ftask.json` for anything that requires a usable root, the entries along any input path, plus whatever its Needed files part lists. A problem with a needed file is an error. A problem with a **relevant** file — one that changes or explains the operation's result, as its Needed files part lists — is a warning. Problems with unrelated files the operation walks past are skipped silently; [`doctor`](design-spec.md#doctor) is the operation for finding those. The design spec's [Walking the tree](design-spec.md#walking-the-tree) applies these rules to blockers, duplicates, and unlistable folders.

A read or write operation reports one error. When several apply, it reports the first in this order:

1. `invalid-input` — checked before anything else is read.
2. `environment` (the config can't be located), then `not-initialized`, and `corrupt` or `unsupported-format` for the config and `ftask.json` — the [Root states](#root-states) errors.
3. `busy`.
4. `not-found`, and `corrupt` (`reason`: `unexpected-file`) for an entry on an input path — whichever the [path walk](#path-walk) meets first.
5. `corrupt` or `unsupported-format` for needed task files.
6. `conflict`.

Steps 4–6 are checked under the write lock. A write re-reads `ftask.json` after acquiring the lock (it decides on current state); a problem found only then is reported with the step 2 kinds. `io` and `internal` are reported wherever they occur. Setup defines its own order (see [`init`](#init)).

When several errors of one step apply and the kind reports only one (e.g. two needed task files are both `corrupt`), the one reported is the first in [tree order](#tree-order). A kind that lists every instance (e.g. `not-found`'s `ids`) lists them all.

A needed task file that the walk found but that has disappeared by the time it is read is treated as never found: `not-found` if it was the only file with that ID. In a write this is possible only through an outside change, since the write lock excludes other writes; in a read, a concurrent write may cause it.

A kind that lists every instance lists them across the whole step: e.g. [`create`](#create) with both a missing `folder` and missing `blocked_by` IDs reports one `not-found` with both `folders` and `ids` filled.

[`block`](#block) reads its own task file(s) before looking up its blockers, since which blockers are new depends on the task's `blocked_by`: an unusable copy of `id` is therefore reported before a missing blocker. When `id` is duplicated, a new blocker is one that is in no copy's `blocked_by`.

### Error schema

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "error",
  "type": "object",
  "required": ["kind", "message", "details"],
  "properties": {
    "kind": { "type": "string", "description": "One of the Error kinds; callers treat unknown values as a generic failure." },
    "message": { "type": "string", "description": "Human-readable explanation. Not part of the contract." },
    "details": { "type": "object", "description": "Kind-specific structured data (see Error kinds)." },
    "partial": { "type": "object", "description": "Present only when a multi-file write or setup failed after some of its effects took place. Shape is defined per operation (see its Partial schema)." }
  },
  "additionalProperties": false,
  "allOf": [
    { "if": { "properties": { "kind": { "const": "invalid-input" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/invalid-input" } } } },
    { "if": { "properties": { "kind": { "const": "environment" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/environment" } } } },
    { "if": { "properties": { "kind": { "const": "not-initialized" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/not-initialized" } } } },
    { "if": { "properties": { "kind": { "const": "not-found" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/not-found" } } } },
    { "if": { "properties": { "kind": { "const": "conflict" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/conflict" } } } },
    { "if": { "properties": { "kind": { "const": "corrupt" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/corrupt" } } } },
    { "if": { "properties": { "kind": { "const": "io" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/io" } } } },
    { "if": { "properties": { "kind": { "const": "unsupported-format" } } }, "then": { "properties": { "details": { "$ref": "#/$defs/unsupported-format" } } } }
  ],
  "$defs": {
    "invalid-input": {
      "type": "object",
      "required": ["problems"],
      "properties": {
        "problems": {
          "type": "array",
          "minItems": 1,
          "items": {
            "type": "object",
            "required": ["field", "reason"],
            "properties": {
              "field": { "type": "string", "description": "JSON Pointer into the input." },
              "reason": { "type": "string", "description": "Human-readable." }
            },
            "additionalProperties": false
          }
        }
      },
      "additionalProperties": false
    },
    "environment": {
      "type": "object",
      "required": ["variable"],
      "properties": { "variable": { "type": "string", "description": "The environment variable ftask needed, e.g. HOME." } },
      "additionalProperties": false
    },
    "not-initialized": {
      "type": "object",
      "required": ["missing"],
      "properties": { "missing": { "type": "string", "enum": ["config", "root", "metadata"] } },
      "additionalProperties": false
    },
    "not-found": {
      "type": "object",
      "required": ["folders", "ids", "paths"],
      "properties": {
        "folders": { "type": "array", "items": { "$ref": "folder-path" } },
        "ids": { "type": "array", "items": { "$ref": "task-file#/properties/id" } },
        "paths": { "type": "array", "items": { "type": "string" } }
      },
      "additionalProperties": false
    },
    "conflict": {
      "type": "object",
      "required": ["rule", "ids"],
      "properties": {
        "rule": { "type": "string", "description": "One of the known conflict rules (see Error kinds); callers treat unknown values as a generic conflict." },
        "ids": { "type": "array", "items": { "$ref": "task-file#/properties/id" } },
        "cycles": {
          "type": "array",
          "minItems": 1,
          "items": { "type": "array", "minItems": 2, "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" } },
          "description": "For rule acyclic: cycles[i] is the cycle for ids[i], as a list of distinct IDs, each task blocked by the next, and the last blocked by the first."
        }
      },
      "additionalProperties": false,
      "if": { "properties": { "rule": { "const": "acyclic" } } },
      "then": { "required": ["cycles"] },
      "else": { "not": { "required": ["cycles"] } }
    },
    "corrupt": {
      "type": "object",
      "required": ["path", "reason"],
      "properties": {
        "path": { "type": "string" },
        "reason": { "type": "string", "enum": ["not-json", "invalid", "unexpected-file"] }
      },
      "additionalProperties": false
    },
    "io": {
      "type": "object",
      "required": ["path", "code"],
      "properties": {
        "path": { "type": "string" },
        "code": { "type": "string", "description": "Symbolic OS error, e.g. ENOSPC." }
      },
      "additionalProperties": false
    },
    "unsupported-format": {
      "type": "object",
      "required": ["path", "found", "supported"],
      "properties": {
        "path": { "type": "string" },
        "found": { "type": "integer", "minimum": -9007199254740991, "maximum": 9007199254740991 },
        "supported": { "type": "array", "items": { "type": "integer" } }
      },
      "additionalProperties": false
    }
  }
}
```

## Warnings

A warning reports a problem, relevant to the operation's result, that did not stop the operation. Operations never fail because of a warning, and never warn about files unrelated to their result (see [Precedence](#precedence)). Which entries produce no warning at all — non-matching entries, hidden entries — is set out in the design spec's [Walking the tree](design-spec.md#walking-the-tree).

- **One warning per distinct problem:** per file for `unusable-file`, per folder for `unreadable-folder`, per ID for `duplicate-id`, per (referring, missing) pair for `dangling-reference`, per `.md` for `notes-missing` — however many tasks the problem affects.
- **Deterministic order:** `warnings` is sorted by `kind`, then by the first entry of `paths`, then by `ids` (compared element by element, as numbers). The same tree always yields the same warnings in the same order.

### Warning kinds

| Kind | Meaning | `paths` | `ids` | Other fields |
|---|---|---|---|---|
| `unusable-file` | A task file was skipped because it is [unusable](design-spec.md#file-validity). | the file | the task, from the filename | `reason`: `unreadable`, `corrupt`, or `unsupported-format`; `code` when `unreadable` |
| `duplicate-id` | Several task files with one ID all exist, and the duplication bears on the result (see [Walking the tree](design-spec.md#walking-the-tree)). | the files in the operation's scope, in [tree order](#tree-order) | the one ID | — |
| `dangling-reference` | A task's `blocked_by` names an ID with no task (see [Dependencies](design-spec.md#dependencies)). Not reported while a folder the walk had to list was unreadable, since the blocker may be in it; the `unreadable-folder` warning explains why the task counts as blocked. | the referring task file | `[referring, missing]`, in that order | — |
| `unreadable-folder` | A folder the walk had to list could not be listed; its tasks are missing from the result (see [Walking the tree](design-spec.md#walking-the-tree)). | the folder's filesystem path | none | `code` |
| `notes-missing` | A task was written but its `.md` could not be. The task is valid; its notes are empty, or stale if a stray `.md` could not be replaced. | the `.md` | the task | `code` |

### Warning schema

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "warning",
  "type": "object",
  "required": ["kind", "message", "paths", "ids"],
  "properties": {
    "kind": { "type": "string", "description": "One of the Warning kinds; callers ignore unknown values." },
    "message": { "type": "string", "description": "Human-readable explanation. Not part of the contract." },
    "paths": { "type": "array", "items": { "type": "string" }, "description": "Filesystem paths involved; meaning per kind (see Warning kinds)." },
    "ids": { "type": "array", "items": { "type": "integer" }, "description": "Task IDs involved; order and meaning per kind (see Warning kinds)." },
    "reason": { "type": "string", "description": "For unusable-file: unreadable, corrupt, or unsupported-format." },
    "code": { "type": "string", "description": "Symbolic OS error (e.g. EACCES), for unusable-file with reason unreadable, unreadable-folder, and notes-missing." }
  },
  "additionalProperties": false,
  "allOf": [
    { "if": { "properties": { "kind": { "const": "unusable-file" } } }, "then": { "required": ["reason"] } },
    { "if": { "properties": { "kind": { "const": "unusable-file" }, "reason": { "const": "unreadable" } }, "required": ["reason"] }, "then": { "required": ["code"] } },
    { "if": { "properties": { "kind": { "const": "notes-missing" } } }, "then": { "required": ["code"] } },
    { "if": { "properties": { "kind": { "const": "unreadable-folder" } } }, "then": { "required": ["code"] } }
  ]
}
```

## Root states

Whether read and write operations can run against this machine's root is one of three states. Finding the root starts with locating the config; when that is impossible, operations that require a usable root fail with `environment` before any state applies (see [Config file](design-spec.md#config-file)).

| State | Holds when | Operations that require a usable root fail with |
|---|---|---|
| ***Not initialized*** | Something is missing: the config, the root it names (the path must lead, through symlinks, to a directory), or `ftask.json` in that root. | `not-initialized`, with `missing` naming the first absent piece. Remedy depends on `missing` — see below. |
| ***Initialized, not usable*** | Nothing is missing, but the config or `ftask.json` is unusable. | `corrupt` or `unsupported-format`, or `io` if the file is unreadable. Remedy: repair the file or its permissions, or use a binary that supports the format. |
| ***Usable*** | Nothing is missing, and the config and `ftask.json` both pass every check in [File validity](design-spec.md#file-validity). | — |

Remedies for *not initialized*, by `missing`:

- **`config`** — nothing is set up on this machine. Run [`init`](#init).
- **`root`** — the config names a root that isn't there. Check the path first (an unmounted drive, a moved folder). Run `init` with `replace_config` only if a new or different tree is really intended: on an unmounted drive's mount point it would create a fresh empty tree.
- **`metadata`** — the root exists but has lost its `ftask.json`. Run [`doctor`](design-spec.md#doctor), which can rebuild it. `init` refuses (the config exists, and the root isn't empty).

*Initialized* is about presence; *usable* additionally about content. A file that exists but is unusable never makes a root *not initialized*. In particular, a config that exists but is unusable — it doesn't parse, or names a root in an illegal [form](design-spec.md#root-path) — leaves the root *initialized, not usable*, even though no root can be read from it; operations fail with `corrupt`.

## Shared rules

### Tree order

The order ftask uses whenever it lists folders or tasks by location:

1. By folder path, a parent before its children, siblings by name: `/`, `/infra`, `/proj`, `/proj/travel`, `/proj-b`. This is *not* a plain string sort of the paths (which would put `/proj-b` before `/proj/travel`, since `-` sorts before `/`).
2. Within a folder, tasks by `id`, lowest first.

### Path walk

How an operation checks a folder path given as input (e.g. `folder`). Entries are checked from the root down, one segment at a time, stopping at the first that is not a plain directory:

- **Missing** → `not-found` (`folders`: that folder path) — or, for `create-folder` with `parents`, created.
- **Present but not a directory, or a symlink** → `corrupt` (`reason`: `unexpected-file`). Symlinks are never followed.
- **A directory** → continue to the next segment.

So a folder "exists" only if every entry on its path is a plain directory, not a symlink.

### Undo

[`delete`](#delete) and [`delete-folder`](#delete-folder) remove files for good: ftask keeps no trash and no history. Undo comes from git, when the root is a repository (see *Syncing and committing are allowed* in [Assumptions](design-spec.md#assumptions)), and reaches back only to the last commit — ftask never commits. Restoring from git is an [outside change](design-spec.md#assumptions):

- **Restore only the removed paths**, never the whole tree: restoring `ftask.json` can lower `last_id` and let IDs be reused. A delete not yet committed is undone with `git restore -- proj/travel`; a committed one from a commit that still has the files, usually the parent of the one that removed them: `git restore --source=<commit>^ -- proj/travel`. A task is two paths, `42.json` and `42.md`.
- **References don't come back.** The delete removed the task's ID from its dependents' `blocked_by`, and restoring their files too would undo any other change to them since. Re-[`block`](#block) the `dependents` the delete reported instead.
- **What ftask would have checked is left to [`doctor`](design-spec.md#doctor):** a restored task's `blocked_by` may name tasks removed since, and its edges may close a cycle added while it was gone.

## Shared schemas

### Task

Every operation that returns a task returns it in this shape: exactly the [task file schema](design-spec.md#task-file-schema), plus two keys saying where the task lives.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "task",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "blocked_by", "tags", "extra", "folder", "notes_path"],
  "properties": {
    "schema": { "$ref": "task-file#/properties/schema" },
    "id": { "$ref": "task-file#/properties/id" },
    "title": { "$ref": "task-file#/properties/title" },
    "priority": { "$ref": "task-file#/properties/priority" },
    "created_at": { "$ref": "task-file#/properties/created_at" },
    "completed_at": { "$ref": "task-file#/properties/completed_at" },
    "blocked_by": { "$ref": "task-file#/properties/blocked_by" },
    "tags": { "$ref": "task-file#/properties/tags" },
    "extra": { "$ref": "task-file#/properties/extra" },
    "folder": { "$ref": "folder-path", "description": "The folder holding the task." },
    "notes_path": { "type": "string", "description": "Absolute filesystem path of the task's .md, for opening in an editor. Built from the root path as stored (see Root path)." }
  },
  "additionalProperties": false
}
```

### Task view

A [Task](#task) plus its derived readiness. Returned by read operations that report readiness (`show`, `frontier`, `list`).

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "task-view",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "blocked_by", "tags", "extra", "folder", "notes_path", "readiness", "blocking"],
  "properties": {
    "schema": { "$ref": "task#/properties/schema" },
    "id": { "$ref": "task#/properties/id" },
    "title": { "$ref": "task#/properties/title" },
    "priority": { "$ref": "task#/properties/priority" },
    "created_at": { "$ref": "task#/properties/created_at" },
    "completed_at": { "$ref": "task#/properties/completed_at" },
    "blocked_by": { "$ref": "task#/properties/blocked_by" },
    "tags": { "$ref": "task#/properties/tags" },
    "extra": { "$ref": "task#/properties/extra" },
    "folder": { "$ref": "task#/properties/folder" },
    "notes_path": { "$ref": "task#/properties/notes_path" },
    "readiness": { "type": "string", "enum": ["ready", "blocked", "complete"], "description": "Derived per Dependencies (see Dependencies)." },
    "blocking": {
      "type": "array",
      "items": { "$ref": "task-file#/properties/id" },
      "uniqueItems": true,
      "description": "IDs in blocked_by that currently block the task — open, missing, unusable, or duplicated — in ascending order. Non-empty exactly when readiness is blocked."
    }
  },
  "additionalProperties": false,
  "allOf": [
    { "if": { "properties": { "readiness": { "const": "blocked" } } }, "then": { "properties": { "blocking": { "minItems": 1 } } }, "else": { "properties": { "blocking": { "maxItems": 0 } } } },
    { "if": { "properties": { "readiness": { "const": "complete" } } }, "then": { "properties": { "completed_at": { "type": "string" } } }, "else": { "properties": { "completed_at": { "type": "null" } } } }
  ]
}
```

### Folder path

A [folder path](design-spec.md#folder-paths).

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "folder-path",
  "type": "string",
  "pattern": "^/$|^(/[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?)+$",
  "description": "Folder path from the root, e.g. /proj/travel."
}
```

## Versioning

ftask releases follow [semantic versioning](https://semver.org/). The operation input, output, partial, error, and warning schemas are ftask's public contract: a breaking change to any of them requires a new major version. There is no separate API version.

Adding a new error kind, warning kind, or `conflict` rule is a **minor** change. Callers must therefore treat an unknown error kind as a generic failure and ignore unknown warning kinds. For the same reason the published schemas type `kind` (and `rule`) as a plain string, not a closed enum; the known values are listed in this document.

Data formats are versioned separately; see the design spec's [Format versions](design-spec.md#format-versions). A release reports its format versions via [`version`](#version).

## Setup and diagnostics

### init

Create a new tree, or attach an existing one, and make it this machine's configured root. A new tree gets a fresh `ftask.json`; an existing tree — one that already contains `ftask.json`, e.g. cloned or moved from another machine — is left untouched, and `init` only writes the config naming it. Afterwards the root is at least *initialized* (see [Root states](#root-states)).

**Kind:** setup. Takes no lock — a new tree's root may not exist until `init` creates it, and `init` never changes an existing tree's content. Does not require a usable root. Concurrent `init` runs are not supported.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "init-input",
  "type": "object",
  "required": ["root"],
  "properties": {
    "root": { "type": "string", "description": "Absolute root path, with no .. segments. Resolving relative paths or ~ is the caller's job." },
    "replace_config": { "type": "boolean", "default": false, "description": "Allow replacing an existing config." }
  },
  "additionalProperties": false
}
```

**Additional validation:** `root` must be an absolute path with no `..` segments (see [Root path](design-spec.md#root-path)). It is cleaned first; every check below runs on the cleaned path, which is also what the config records. If anything exists at that path, it must lead (through symlinks) to a directory; a regular file or a dangling symlink there is `invalid-input`.

**Preconditions:**

| State of `root` | Outcome |
|---|---|
| Does not exist, parent directory exists | Created as a new tree. |
| Does not exist, parent directory missing | `not-found`. `init` never creates the root's parent directories. |
| Empty directory (hidden entries allowed, e.g. `.git`) | Initialized as a new tree. |
| Non-empty directory without `ftask.json` | `conflict` (`rule`: `root-not-empty`) — ftask never adopts a directory with other contents. |
| Directory containing `ftask.json` | Attached as-is. |

If a config already exists — whatever it names, and whether or not it parses — and `replace_config` is false: `conflict` (`rule`: `config-exists`). `init` never compares roots and never overwrites a config unless told to.

**Needed files:** `ftask.json` in `root`, when present, checked as [File validity](design-spec.md#file-validity) describes. `init` never reads the config; it only checks whether one exists.

**Effects:**

- `root` is a directory containing `ftask.json`. A new tree has `{"schema": 1, "last_id": 0}`; an existing tree's `ftask.json` is unchanged.
- The config names `root`. `init` creates the config directory, and any missing ancestors of it, as needed.

**Invariants at risk:** none. `init` never modifies an existing tree's content; creating a new tree produces an empty tree, which satisfies every invariant.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "init-output",
  "type": "object",
  "required": ["root", "action", "last_id"],
  "properties": {
    "root": { "type": "string", "description": "Absolute root path, as recorded in the config." },
    "action": { "type": "string", "enum": ["created", "attached"], "description": "created: a new, empty tree was created. attached: an existing tree (one already containing ftask.json) was attached to this machine." },
    "last_id": { "$ref": "root-file#/properties/last_id" }
  },
  "additionalProperties": false
}
```

**Errors,** in `init`'s own precedence order:

| Kind | When |
|---|---|
| `invalid-input` | `root` is not absolute, contains `..`, or something exists there that doesn't lead to a directory. |
| `environment` | The config can't be located (see [Config file](design-spec.md#config-file)). |
| `conflict` | (`rule`: `config-exists`) A config already exists and `replace_config` is false. |
| `not-found` | `root` does not exist and neither does its parent directory (the parent in `paths`). |
| `conflict` | (`rule`: `root-not-empty`) `root` is a non-empty directory without `ftask.json`. |
| `corrupt` | `ftask.json` exists but is corrupt, including not being a regular file. |
| `unsupported-format` | `ftask.json` exists with an unsupported `schema`. |

**Warnings:** none.

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "init-partial",
  "type": "object",
  "required": ["root_created", "metadata_created"],
  "properties": {
    "root_created": { "type": "boolean", "description": "init created the root directory." },
    "metadata_created": { "type": "boolean", "description": "init created ftask.json." }
  },
  "additionalProperties": false
}
```

Present when `init` fails after creating the root directory or `ftask.json` — e.g. the config cannot be written. What it created stays; rerunning `init` with the same input completes it.

**Crash behavior:** a crash may leave some of the pieces in place and not others — e.g. a new root directory without `ftask.json`, or a tree with `ftask.json` but no config. The config is written last, so until it exists nothing else uses the root, and a partial `init` is never mistaken for a usable one. Apart from possible leftover temp files — in the root, which `doctor` finds, or in the config directory, which the next `init` removes — there is nothing for `doctor` to find.

**Retry safety:** after a crash or an error with `partial`, safe: the config is written last, so an interrupted `init` did not change the config, and rerunning it with the same input finishes the job (reporting `attached` if it had already written `ftask.json` — an empty tree it created itself). After a success, rerunning fails with `conflict` (`rule`: `config-exists`). A crash after the config is written — while only its temp file remains to remove — is a success, and a rerun fails the same way.

### version

Report the version and build of the ftask binary, and the data format versions it supports. Describes the binary only, never a root.

**Kind:** read. Takes no lock. Does not require a usable root, so it works before `init` and whatever the root state.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "version-input",
  "type": "object",
  "properties": {},
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** none.

**Needed files:** none.

**Effects:** none.

**Invariants at risk:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "version-output",
  "type": "object",
  "required": ["version", "commit", "commit_time", "uncommitted_changes", "go", "platform", "schemas"],
  "properties": {
    "version": { "type": "string", "description": "Release version (semver), e.g. 1.4.0." },
    "commit": { "type": "string", "description": "Source commit the binary was built from." },
    "commit_time": { "$ref": "task-file#/$defs/timestamp", "description": "Timestamp of that commit." },
    "uncommitted_changes": { "type": "boolean", "description": "True if the binary was built from a working tree with uncommitted changes, so it does not exactly match commit." },
    "go": { "type": "string", "description": "Go toolchain the binary was built with, e.g. go1.25.1." },
    "platform": { "type": "string", "description": "OS and architecture, e.g. linux/amd64." },
    "schemas": {
      "type": "object",
      "required": ["task", "root"],
      "properties": {
        "task": { "type": "integer", "description": "The one task file format version this binary reads and writes." },
        "root": { "type": "integer", "description": "The one ftask.json format version this binary reads and writes." }
      },
      "additionalProperties": false
    }
  },
  "additionalProperties": false
}
```

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | The input has any property (the schema allows none). |

**Warnings:** none.

**Partial schema:** none.

**Crash behavior:** none. `version` changes nothing.

**Retry safety:** safe.

### info

Report the state of this machine's configured root: what is configured, what exists, and whether it is usable by this binary. Diagnostic by design — it reports a root that is not initialized or not usable as state, rather than failing on it.

**Kind:** read. Takes no lock. Does not require a usable root. Inspects a fixed set of files only; it never walks the tree, so its cost does not grow with the number of tasks.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "info-input",
  "type": "object",
  "properties": {},
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** none.

**Needed files:** none. `info` inspects the config and `ftask.json` but reports any problem with them as state, not as an error.

**Effects:** none.

**Invariants at risk:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "info-output",
  "type": "object",
  "required": ["config", "tree", "initialized", "usable", "compatible"],
  "properties": {
    "config": {
      "type": "object",
      "required": ["path", "state", "root"],
      "properties": {
        "path": { "type": ["string", "null"], "description": "Path of the config; null when it can't be located (see Config file)." },
        "state": { "type": "string", "enum": ["missing", "unreadable", "corrupt", "ok"], "description": "State of the config; missing also when it can't be located." },
        "root": { "type": ["string", "null"], "description": "Root path named by the config, cleaned and with ~/ expanded (the same form as notes_path); null if the config is missing, unreadable, doesn't parse, or names a root in an illegal form." }
      },
      "additionalProperties": false
    },
    "tree": {
      "type": ["object", "null"],
      "required": ["root_exists", "metadata", "schema", "last_id"],
      "properties": {
        "root_exists": { "type": "boolean", "description": "Whether the root path leads, through symlinks, to a directory." },
        "metadata": { "type": "string", "enum": ["missing", "unreadable", "corrupt", "unsupported-format", "ok"], "description": "State of ftask.json, per the three-step check (see File validity): corrupt fails step 1 or 3 (or is not a regular file); unsupported-format fails step 2." },
        "schema": { "type": ["integer", "null"], "minimum": -9007199254740991, "maximum": 9007199254740991, "description": "ftask.json's schema value; set whenever step 1 passes — whether metadata ends up ok, unsupported-format, or corrupt at step 3 — otherwise null." },
        "last_id": { "anyOf": [{ "$ref": "root-file#/properties/last_id" }, { "type": "null" }], "description": "Highest task ID issued; null unless metadata is ok." }
      },
      "additionalProperties": false,
      "description": "State of the configured root; null when config.root is null."
    },
    "initialized": { "type": "boolean", "description": "True unless the root is not initialized (see Root states). True, with tree null, when the config exists but is unusable." },
    "usable": { "type": "boolean", "description": "True when the root is usable (see Root states)." },
    "compatible": { "type": ["boolean", "null"], "description": "Whether tree.schema equals this binary's supported ftask.json version (see Format versions); null when tree.schema is null." }
  },
  "additionalProperties": false
}
```

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | The input has any property (the schema allows none). |

Every problem with the root is reported as state in the output, not as an error.

**Warnings:** none.

**Partial schema:** none.

**Crash behavior:** none. `info` changes nothing.

**Retry safety:** safe.

## Folder operations

### create-folder

Create a folder, and optionally any missing parent folders.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "create-folder-input",
  "type": "object",
  "required": ["folder"],
  "properties": {
    "folder": { "$ref": "folder-path" },
    "parents": { "type": "boolean", "default": false, "description": "Create missing parent folders instead of failing." }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:**

| State of the path | Outcome |
|---|---|
| The [path walk](#path-walk) finds every entry, including `folder`, a plain directory | Nothing to do; succeeds with `created` empty. `/` always exists. |
| It stops at `folder` itself, missing | `folder` is created. |
| It stops at a missing parent, `parents` false | `not-found` (`folders`: that parent — the outermost missing one). |
| It stops at a missing parent, `parents` true | That folder and every folder below it on the path are created, outermost first. |
| It stops at an entry that is not a directory, or is a symlink — including `folder` itself | `corrupt` (`reason`: `unexpected-file`). A symlink is never followed: creating through it would place the folder outside the tree. |

**Needed files:** the entries along `folder`'s path, checked by the [path walk](#path-walk). `create-folder` does not walk the tree.

**Effects:** `folder` exists as a directory, along with every folder above it.

**Invariants at risk:** none. Folders carry no invariants.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "create-folder-output",
  "type": "object",
  "required": ["folder", "created"],
  "properties": {
    "folder": { "$ref": "folder-path" },
    "created": { "type": "array", "items": { "$ref": "folder-path" }, "description": "Folders this operation created, outermost first; empty if folder already existed." }
  },
  "additionalProperties": false
}
```

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `folder` is not a valid folder path. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found`, `corrupt` | Whichever the [path walk](#path-walk) meets first: a missing parent while `parents` is false (`not-found`, `folders`: the outermost missing parent); an entry that is not a directory, or is a symlink (`corrupt`, `reason`: `unexpected-file`). |

**Warnings:** none.

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "create-folder-partial",
  "type": "object",
  "required": ["created"],
  "properties": {
    "created": { "type": "array", "items": { "$ref": "folder-path" }, "description": "Folders created before the failure, outermost first." }
  },
  "additionalProperties": false
}
```

Present only when an error (e.g. `io`) interrupts a `parents` chain after at least one folder was created. The created folders stay.

**Crash behavior:** creating one folder is a single atomic step. With `parents`, a crash can leave some of the missing folders created and not others. Empty folders are valid, so the tree satisfies every invariant and there is nothing for `doctor` to find.

**Retry safety:** safe. An existing folder is not an error, so rerunning with the same input completes any interrupted chain and otherwise does nothing.

### delete-folder

Permanently remove a folder and everything under it, and remove the IDs of the tasks under it from every `blocked_by` outside it. ftask keeps no copy; see [Undo](#undo).

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "delete-folder-input",
  "type": "object",
  "required": ["folder"],
  "properties": {
    "folder": { "$ref": "folder-path" },
    "recursive": { "type": "boolean", "default": false, "description": "Delete the folder even if it holds tasks or folders." }
  },
  "additionalProperties": false
}
```

**Additional validation:** `folder` is not `/`: the root can never be deleted (see [Folders](design-spec.md#folders)).

**Preconditions:**

- `folder` exists.
- Without `recursive`, `folder` holds no task and no folder. Entries that count as neither — hidden entries, editor side files, a `.md` without a task file — don't make it non-empty, and are removed with it.
- No task under `folder` has an ID with more than one task file, anywhere in the tree. A write must know which task it removes.
- No task under `folder` has an ID above `last_id`: that state only arises from a system crash or an outside change, and removing the task would let its ID be reissued undetectably (see [Task IDs](design-spec.md#task-ids)).

The tasks under `folder` may be open or complete, and their task files may be unusable: `delete-folder` needs only their filenames, never their contents.

**Needed files:** the entries along `folder`'s path ([path walk](#path-walk)); the names of every entry under `folder`, at every depth; and, outside `folder`, every task file whose `blocked_by` names a task under it — those are rewritten. It lists everything under `folder` first, and fails with `io` if it meets a folder there it can't list: it must know every ID it removes. If `folder` holds a task, it then walks the rest of the tree — there is no index — and fails with `io` on a folder it can't list there too, since it must find every reference; if `folder` holds none, nothing outside it is read. Relevant files: every other task file outside `folder` — one that is unusable may hold a reference that can't be removed, so it is a warning, and the reference is left for [`doctor`](design-spec.md#doctor).

**Effects:**

- Every task file outside `folder` whose `blocked_by` contains an ID of a task under `folder` has those IDs removed. Every other field, the task file's `schema`, and the `.md` are unchanged.
- `folder` no longer exists, nor does anything under it: its tasks and their notes, its folders, and every other entry, hidden ones included.

Tasks that were blocked only by tasks under `folder` become ready, as they would if those tasks had been completed. References between tasks under `folder` go with it.

**Invariants at risk:**

- *No dangling references* — every reference to a removed task is removed, under the write lock, before the folder is.
- *Unique IDs*, *IDs within `last_id`* — removing tasks cannot break either; the `last_id` precondition keeps a removed ID from being reissued.
- *Acyclic* — cannot be broken: removing edges and tasks cannot create a cycle.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "delete-folder-output",
  "type": "object",
  "required": ["folder", "folders", "ids", "dependents"],
  "properties": {
    "folder": { "$ref": "folder-path", "description": "The folder deleted." },
    "folders": { "type": "array", "minItems": 1, "items": { "$ref": "folder-path" }, "description": "Every folder removed — folder itself and each folder under it — in tree order." },
    "ids": { "type": "array", "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "The IDs of the tasks removed, in ascending order; empty if the folder held none." },
    "dependents": { "type": "array", "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "Tasks outside folder whose blocked_by lost at least one of ids, in ascending order." }
  },
  "additionalProperties": false
}
```

`ids` is what to look for in git to restore a task; `dependents` is what to re-`block` after restoring one (see [Undo](#undo)).

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `folder` is not a valid folder path, or is `/`. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found`, `corrupt` | `folder` fails the [path walk](#path-walk) (`not-found`, `folders`: the outermost missing folder; or `corrupt`, `reason`: `unexpected-file`). |
| `conflict` | (`rule`: `not-empty`) `recursive` is false and `folder` holds a task or folder. `ids`: the tasks under `folder`, ascending (empty if it holds only folders). |
| `conflict` | (`rule`: `duplicate-id`) A task under `folder` has an ID with more than one task file. `ids`: every such ID, ascending. |
| `conflict` | (`rule`: `id-above-last-id`) A task under `folder` has an ID above `last_id`. `ids`: every such ID, ascending. Repair with [`doctor`](design-spec.md#doctor) first. |

The `conflict` rules are checked in the order listed; the first that applies is reported.

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A task file outside `folder` is unusable, so any reference it holds to a removed task can't be removed. |

Every unusable task file outside `folder` is reported, not only those known to reference a removed task: an unusable file's `blocked_by` can't be read, so any of them may be one. None is reported when `folder` holds no task, since there is then no reference to remove.

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "delete-folder-partial",
  "type": "object",
  "required": ["dependents"],
  "properties": {
    "dependents": { "type": "array", "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "Tasks whose references were removed before the failure, in ascending order. The folder was not removed." }
  },
  "additionalProperties": false
}
```

Present only when an error (e.g. `io`) comes after at least one dependent was rewritten. The folder and everything under it are still in place; the rewritten dependents stay rewritten. Removing a reference to a task that still exists breaks no invariant, so the tree is valid.

**Crash behavior:** steps run in this order:

1. Each dependent is rewritten, one file at a time. A process crash here leaves some references removed and the folder in place — a valid tree.
2. `folder` is renamed to a hidden temp name in the root — one atomic step, however large the folder. From here the folder is gone from the tree, and the operation has succeeded.
3. The temp folder is removed. A process crash, or an error, here leaves a hidden leftover, which reads ignore and `doctor` reports; the operation still succeeds.

After a [system crash](design-spec.md#crashes) the ordering may not survive: the rename can persist while a dependent's rewrite is lost, leaving a dangling reference for `doctor`.

**Retry safety:** after `busy`, an error with `partial`, or a crash before step 2, safe: rerunning removes what is left. After success, rerunning fails with `not-found`.

### move-folder

Move a folder, and everything under it, to a new place in the tree — which also renames it. Like `mv`: into `to` if `to` is an existing folder, otherwise to the path `to`.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "move-folder-input",
  "type": "object",
  "required": ["folder", "to"],
  "properties": {
    "folder": { "$ref": "folder-path", "description": "The folder to move." },
    "to": { "$ref": "folder-path", "description": "An existing folder to move it into, or its new path." },
    "parents": { "type": "boolean", "default": false, "description": "Create missing folders above the new path instead of failing." }
  },
  "additionalProperties": false
}
```

**Additional validation:**

- `folder` is not `/`: the root can never be moved.
- `to` is neither `folder` nor under it: a folder can't be moved into itself.

**Preconditions:** `folder` exists. Where it goes — its **target** — depends on `to`:

| State of `to` | Target |
|---|---|
| The [path walk](#path-walk) finds `to`, a plain directory | `to` + `/` + `folder`'s name: `/proj/travel` into `/archive` is `/archive/travel`. |
| It stops at `to` itself, missing | `to`: `/proj/travel` to `/archive/travel-2025` moves and renames it. |
| It stops at a folder above `to`, missing, `parents` false | `not-found` (`folders`: the outermost missing folder). |
| It stops at a folder above `to`, missing, `parents` true | `to`. That folder and every folder below it on the path, up to but not including `to`, are created, outermost first. |
| It stops at an entry that is not a directory, or is a symlink — including `to` itself | `corrupt` (`reason`: `unexpected-file`). |

Then:

- **Target is `folder` itself** (e.g. `/proj/travel` into `/proj`): nothing to do; succeeds with `changed` false.
- **Anything exists at the target** — a folder, a file, a symlink: `conflict` (`rule`: `destination-exists`). Folders are never merged.

**Needed files:** the entries along `folder`'s and `to`'s paths, checked by the [path walk](#path-walk) — `folder` first — and the entry at the target. `move-folder` does not walk the tree and reads no task file: a task's identity is its ID, not its location, so nothing inside the folder changes.

**Effects:** `folder`, with everything under it, is at the target. Every task under it keeps its ID and fields; only its folder changes. With `parents`, any missing folders above the target exist.

**Invariants at risk:** none. `blocked_by` names tasks by ID, so moving them changes no reference.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "move-folder-output",
  "type": "object",
  "required": ["folder", "from", "created", "changed"],
  "properties": {
    "folder": { "$ref": "folder-path", "description": "The folder's path after the operation: the target." },
    "from": { "$ref": "folder-path", "description": "The folder's path before the operation." },
    "created": { "type": "array", "items": { "$ref": "folder-path" }, "description": "Folders created above the target (parents), outermost first; empty if none." },
    "changed": { "type": "boolean", "description": "True if the folder moved; false if it was already at the target." }
  },
  "additionalProperties": false
}
```

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `folder` or `to` is not a valid folder path, `folder` is `/`, or `to` is `folder` or under it. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found`, `corrupt` | Whichever the [path walk](#path-walk) meets first, `folder`'s path before `to`'s: a missing `folder`, or a missing folder above `to` while `parents` is false (`not-found`, `folders`: every missing one, both paths together); an entry that is not a directory, or is a symlink (`corrupt`, `reason`: `unexpected-file`). |
| `conflict` | (`rule`: `destination-exists`) Something already exists at the target (`ids`: `[]`). |

**Warnings:** none.

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "move-folder-partial",
  "type": "object",
  "required": ["created"],
  "properties": {
    "created": { "type": "array", "items": { "$ref": "folder-path" }, "description": "Folders created before the failure, outermost first. The folder did not move." }
  },
  "additionalProperties": false
}
```

Present only when an error (e.g. `io`) comes after `parents` created at least one folder. The created folders stay.

**Crash behavior:** moving the folder is one `rename`, so it is either at its old path or at the target, whole. With `parents`, a crash can leave some of the missing folders created and the folder not yet moved. Empty folders are valid, so there is nothing for `doctor` to find.

**Retry safety:** after `busy`, an error with `partial`, or a crash, safe: folders already created are not an error, and a folder not yet moved is moved. After success, rerunning fails with `not-found`, since `folder` is gone; a caller unsure whether the move happened checks the target with [`list`](#list).

## Task operations

### create

Create a new, open task.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "create-input",
  "type": "object",
  "required": ["title"],
  "properties": {
    "title": { "type": "string", "description": "Untrimmed; trimmed and validated per Titles (see Additional validation)." },
    "folder": { "$ref": "folder-path", "default": "/" },
    "priority": { "$ref": "task-file#/properties/priority", "default": null },
    "tags": { "$ref": "task-file#/properties/tags", "default": [] },
    "blocked_by": { "$ref": "task-file#/properties/blocked_by", "default": [] },
    "extra": { "$ref": "task-file#/properties/extra", "default": {} },
    "notes": { "type": "string", "default": "", "description": "Initial notes, written to the task's .md." }
  },
  "additionalProperties": false
}
```

**Additional validation:** `title` is trimmed, then validated, per [Titles](design-spec.md#titles). `id`, `schema`, `created_at`, and `completed_at` are never input — ftask sets them.

**Preconditions:** `folder` exists; every ID in `blocked_by` names an existing task, open or complete. A `blocked_by` ID that has several task files exists.

**Needed files:** the entries along `folder`'s path ([path walk](#path-walk)), and the task files whose filename ID is in `blocked_by`. When `blocked_by` is non-empty, `create` walks the whole tree to look its IDs up, and fails with `io` if it meets a folder it can't list; when `blocked_by` is empty, `create` does not walk the tree. Relevant files: the same task files — an ID in `blocked_by` with more than one task file is a warning, not an error.

**Effects:**

- `last_id` is incremented; the new task's `id` is the new `last_id`.
- A task exists in `folder` with the given fields, `schema` set to the supported task file format, `created_at` set to now, and `completed_at` `null`.
- Its `.md` exists, containing `notes` (empty if none were given). A stray `.md` already at that path (left by an editor, or by a system crash) is replaced. If writing the `.md` fails, `create` still succeeds, with a `notes-missing` warning.

**Invariants at risk:**

- *Unique IDs* and *IDs within `last_id`* — the ID is allocated from `last_id` under the write lock. The exception is a tree where a system crash or an outside change left a task with an ID above `last_id`; see Crash behavior.
- *No dangling references* — `blocked_by` is checked against the tree under the write lock.
- *Acyclic* — cannot be broken: no task depends on the new one yet, so it cannot close a cycle.

**Output schema:** the created task, per the [Task](#task) schema.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | Any input fails validation (bad title, tag, folder path, types, non-integer literals, duplicate keys). |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found`, `corrupt` | `folder` fails the [path walk](#path-walk) (`not-found`, or `corrupt` with `reason` `unexpected-file`); or an ID in `blocked_by` has no task file (`not-found`). A missing folder and missing blockers are reported in one `not-found`. |
| `corrupt`, `unsupported-format` | A needed task file is corrupt or has an unsupported `schema`; or the new task's task file already exists in `folder` (`corrupt`, `reason`: `unexpected-file`). |
| `conflict` | (`rule`: `id-exhausted`) The next ID would exceed the [ID ceiling](design-spec.md#task-ids). |

**Warnings:**

| Kind | When |
|---|---|
| `notes-missing` | The task was created but its `.md` could not be written. The operation still succeeds and returns the task. |
| `duplicate-id` | An ID in `blocked_by` has more than one task file. |

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "create-partial",
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": { "type": "integer", "description": "The ID consumed from last_id. This operation created no task; retrying is safe and uses a new ID." }
  },
  "additionalProperties": false
}
```

Present only when the failure came after `last_id` was incremented. Once the task file is written, `create` cannot fail: anything that goes wrong afterwards — writing the `.md` (a `notes-missing` warning), removing a temp file (no warning; `doctor` finds it) — leaves the operation successful. So a `partial` always means this operation consumed an ID and created no task. It does not mean no task has that ID: see Crash behavior.

**Crash behavior:** after a [process crash](design-spec.md#crashes), the tree satisfies every invariant, leaving nothing for `doctor` beyond a possible leftover temp file, because steps run in this order:

1. `last_id` is incremented. A process crash here consumes an ID without creating a task — an allowed gap.
2. The task file is created. A process crash here leaves a valid task whose `.md` is missing, which reads as empty notes.
3. The `.md` is created.

After a [system crash](design-spec.md#crashes), the ordering may not survive: the task file can persist while the `last_id` increment is lost. (An outside change, such as a merge of `ftask.json`, can produce the same state.) The task then has an ID above `last_id`, and the next `create` issues that ID again:

- **In a different folder**, it succeeds, producing two tasks with one ID. Reads report them as `duplicate-id`.
- **In the same folder**, the existing task file blocks it: `create` fails with `corrupt` (`reason`: `unexpected-file`) and a `partial` for the consumed ID. A retry uses the next ID and succeeds.

`doctor` finds the state before or after reuse (a task whose ID exceeds `last_id`; two task files with the same ID).

**Retry safety:** after `busy`, safe — nothing happened. After an error with `partial`, safe — `partial` confirms this operation created no task. After a crash or an unclear outcome, **not** safe: the task may already exist, and retrying creates a duplicate with a new ID. Callers that retry should first check whether the task was created. See [Idempotent create](design-spec.md#idempotent-create).

### show

Return one task by ID: its fields, where it lives (including `notes_path`, for opening the notes in an editor), and its readiness.

**Kind:** read. Takes no lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "show-input",
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id" }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** a task with `id` exists.

**Needed files:** every task file whose filename ID is `id`. There is no index, so `show` walks the whole tree — which is also the only way to find duplicates — and fails with `io` if it meets a folder it can't list. Its scope is the whole tree, so every copy of `id` is returned. Relevant files, whose problems are warnings: if the task is open, the task files of every ID in its `blocked_by`. `show` does not open the `.md`; it only reports `notes_path`.

**Effects:** none.

**Invariants at risk:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "show-output",
  "type": "object",
  "required": ["tasks"],
  "properties": {
    "tasks": {
      "type": "array",
      "minItems": 1,
      "description": "The task with the requested ID: one item normally; every copy, in tree order, when the ID is duplicated.",
      "items": { "$ref": "task-view" }
    }
  },
  "additionalProperties": false
}
```

**Order:** copies of a duplicated ID in [tree order](#tree-order).

Each item is a [Task view](#task-view). Notes are not included; `notes_path` locates them, so output stays small whatever the notes' size. When the ID is duplicated, each copy's readiness is derived from its own `blocked_by`.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `id` is missing or not a valid task ID. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `not-found` | No task file has ID `id` (`ids`: `[id]`). |
| `corrupt`, `unsupported-format` | A task file with ID `id` is unusable — including one copy of a duplicated ID. |

**Warnings:**

| Kind | When |
|---|---|
| `duplicate-id` | `id` has more than one task file; or, for an open task, an ID in its `blocked_by` does. |
| `dangling-reference` | The task is open and an ID in its `blocked_by` has no task file. |
| `unusable-file` | The task is open and a blocker's task file is unusable; the blocker counts as blocking. |

**Partial schema:** none.

**Crash behavior:** none. `show` changes nothing.

**Retry safety:** safe.

### complete

Mark a task complete by setting its `completed_at` to the current time. Completing an already complete task changes nothing.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "complete-input",
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id" }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** exactly one task file has ID `id`. The task may be open or complete, and its blockers may be open — a task can be completed at any time (see [Dependencies](design-spec.md#dependencies)).

**Needed files:** every task file whose filename ID is `id`. There is no index, so `complete` walks the whole tree to find it — which also finds duplicates — and fails with `io` if it meets a folder it can't list. No relevant files beyond that: `complete` reads neither the task's blockers nor its dependents.

**Effects:**

- If the task is open: its `completed_at` is set to the current time. Every other field, the task file's `schema`, and the `.md` are unchanged.
- If the task is already complete: nothing changes; `completed_at` keeps its original value.

Completing a task can make its dependents ready. Readiness is always derived when read (see [Dependencies](design-spec.md#dependencies)), so no other file is rewritten.

**Invariants at risk:** none. `completed_at` takes part in no invariant.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "complete-output",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "blocked_by", "tags", "extra", "folder", "notes_path", "changed"],
  "properties": {
    "schema": { "$ref": "task#/properties/schema" },
    "id": { "$ref": "task#/properties/id" },
    "title": { "$ref": "task#/properties/title" },
    "priority": { "$ref": "task#/properties/priority" },
    "created_at": { "$ref": "task#/properties/created_at" },
    "completed_at": { "$ref": "task#/properties/completed_at" },
    "blocked_by": { "$ref": "task#/properties/blocked_by" },
    "tags": { "$ref": "task#/properties/tags" },
    "extra": { "$ref": "task#/properties/extra" },
    "folder": { "$ref": "task#/properties/folder" },
    "notes_path": { "$ref": "task#/properties/notes_path" },
    "changed": { "type": "boolean", "description": "True if this operation completed the task; false if it was already complete." }
  },
  "additionalProperties": false
}
```

The task after the operation, per the [Task](#task) schema, plus `changed`.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `id` is missing or not a valid task ID. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found` | No task file has ID `id` (`ids`: `[id]`). |
| `corrupt`, `unsupported-format` | The task file is unusable. |
| `conflict` | (`rule`: `duplicate-id`) More than one task file has ID `id` (`ids`: `[id]`). A write must know which task it changes; repair the duplicate with [`doctor`](design-spec.md#doctor) first. |

**Warnings:** none.

**Partial schema:** none. `complete` replaces a single file, all-or-nothing.

**Crash behavior:** the task file is either the old version or the new one. There is nothing for `doctor` to find beyond a possible leftover temp file.

**Retry safety:** safe. Rerunning on a task that is now complete changes nothing and returns `changed: false`.

### reopen

Reopen a complete task by clearing its `completed_at`. Reopening an already open task changes nothing. The counterpart of [`complete`](#complete).

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "reopen-input",
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id" }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** exactly one task file has ID `id`. The task may be open or complete.

**Needed files:** every task file whose filename ID is `id`. There is no index, so `reopen` walks the whole tree to find it — which also finds duplicates — and fails with `io` if it meets a folder it can't list. No relevant files beyond that: `reopen` reads neither the task's blockers nor its dependents.

**Effects:**

- If the task is complete: its `completed_at` is set to `null`. Every other field, the task file's `schema`, and the `.md` are unchanged.
- If the task is already open: nothing changes.

Reopening a task can make its dependents blocked again, and the reopened task itself may be ready or blocked. Readiness is derived when read (see [Dependencies](design-spec.md#dependencies)), so no other file is rewritten.

**Invariants at risk:** none. `completed_at` takes part in no invariant; *Acyclic* already holds for complete tasks, so reopening one cannot expose a cycle.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "reopen-output",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "blocked_by", "tags", "extra", "folder", "notes_path", "changed"],
  "properties": {
    "schema": { "$ref": "task#/properties/schema" },
    "id": { "$ref": "task#/properties/id" },
    "title": { "$ref": "task#/properties/title" },
    "priority": { "$ref": "task#/properties/priority" },
    "created_at": { "$ref": "task#/properties/created_at" },
    "completed_at": { "$ref": "task#/properties/completed_at" },
    "blocked_by": { "$ref": "task#/properties/blocked_by" },
    "tags": { "$ref": "task#/properties/tags" },
    "extra": { "$ref": "task#/properties/extra" },
    "folder": { "$ref": "task#/properties/folder" },
    "notes_path": { "$ref": "task#/properties/notes_path" },
    "changed": { "type": "boolean", "description": "True if this operation reopened the task; false if it was already open." }
  },
  "additionalProperties": false
}
```

The task after the operation, per the [Task](#task) schema, plus `changed`.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `id` is missing or not a valid task ID. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found` | No task file has ID `id` (`ids`: `[id]`). |
| `corrupt`, `unsupported-format` | The task file is unusable. |
| `conflict` | (`rule`: `duplicate-id`) More than one task file has ID `id` (`ids`: `[id]`). A write must know which task it changes; repair the duplicate with [`doctor`](design-spec.md#doctor) first. |

**Warnings:** none.

**Partial schema:** none. `reopen` replaces a single file, all-or-nothing.

**Crash behavior:** the task file is either the old version or the new one. There is nothing for `doctor` to find beyond a possible leftover temp file.

**Retry safety:** safe. Rerunning on a task that is now open changes nothing and returns `changed: false`.

### block

Add one or more blockers to a task's `blocked_by`. All-or-nothing: every blocker is checked before anything is written, and if any check fails, nothing changes.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "block-input",
  "type": "object",
  "required": ["id", "blockers"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id", "description": "The task to block." },
    "blockers": { "type": "array", "minItems": 1, "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "IDs to add to the task's blocked_by." }
  },
  "additionalProperties": false
}
```

**Additional validation:** `blockers` must not contain `id` — a task blocking itself is detectable from the input alone, so it is `invalid-input`, not a `conflict`.

**Preconditions:**

- Exactly one task file has ID `id`. The task may be open or complete.
- Every **new** blocker — an ID in `blockers` not already in the task's `blocked_by` — names an existing task, open or complete. A blocker ID with several task files exists.
- Adding the new blockers creates no cycle.

Blockers already present are no-ops: they are neither checked for existence nor for cycles. A dangling or cyclic blocker already in `blocked_by` (after a system crash or an outside change) is left for [`doctor`](design-spec.md#doctor).

**Needed files:** The whole tree is walked (there is no index); a folder that can't be listed fails with `io`, since a write must *prove* the result acyclic. Needed:

- the task file(s) with ID `id`;
- every task file reachable from the new blockers by following `blocked_by`, stopping at `id` — the cycle check reads exactly these. The walk follows every task on its path, **open or complete** (*Acyclic* holds for both). An unusable file on the way could hide a cycle, so it is an error. A duplicated ID on the way is followed through every copy (the graph is keyed by ID; see [Dependencies](design-spec.md#dependencies)). `id` itself is never expanded, so nothing beyond `id` is read. Every other reachable task file is read, even when a cycle is found early: which files are needed depends only on the graph, not on the order of any search.

Relevant files: the new blockers' own task files — a blocker ID with more than one task file is a `duplicate-id` warning. A `blocked_by` ID further down the chain that names no task has no edges, so cannot lie on a cycle; it is skipped silently.

How the cycle check decides: adding "`id` is blocked by B" creates a cycle exactly when `id` is already reachable from B by following `blocked_by`. Each new edge starts at `id`, so no cycle can pass through two of them: each blocker is judged on its own, and the result doesn't depend on their order. Every offending blocker is found, not just the first. For each, the cycle reported is the **shortest**, ties broken by the lexicographically smallest sequence of IDs (IDs compared as numbers); it is written starting at `id`, then the blocker, then the path back.

**Effects:**

- The task's `blocked_by` becomes `blocked_by` ∪ `blockers`. Blockers already present are left as they are.
- Every other field, the task file's `schema`, and the `.md` are unchanged.

The task may become blocked; readiness is derived when read (see [Dependencies](design-spec.md#dependencies)), so no other file is rewritten.

**Invariants at risk:**

- *Acyclic* — the cycle check above, under the write lock.
- *No dangling references* — every blocker is confirmed to exist, under the write lock.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "block-output",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "blocked_by", "tags", "extra", "folder", "notes_path", "added"],
  "properties": {
    "schema": { "$ref": "task#/properties/schema" },
    "id": { "$ref": "task#/properties/id" },
    "title": { "$ref": "task#/properties/title" },
    "priority": { "$ref": "task#/properties/priority" },
    "created_at": { "$ref": "task#/properties/created_at" },
    "completed_at": { "$ref": "task#/properties/completed_at" },
    "blocked_by": { "$ref": "task#/properties/blocked_by" },
    "tags": { "$ref": "task#/properties/tags" },
    "extra": { "$ref": "task#/properties/extra" },
    "folder": { "$ref": "task#/properties/folder" },
    "notes_path": { "$ref": "task#/properties/notes_path" },
    "added": { "type": "array", "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "The blockers this operation added, in ascending order; empty if all were already present." }
  },
  "additionalProperties": false
}
```

The task after the operation, per the [Task](#task) schema, plus `added`.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `id` or `blockers` is missing or invalid, `blockers` is empty or repeats an ID, or `blockers` contains `id`. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found` | `id`, or a blocker, has no task file (`ids`: every missing one). |
| `corrupt`, `unsupported-format` | The task file, or a task file the cycle check reaches, is unusable. |
| `conflict` | (`rule`: `duplicate-id`) More than one task file has ID `id` (`ids`: `[id]`). A write must know which task it changes; repair the duplicate with [`doctor`](design-spec.md#doctor) first. |
| `conflict` | (`rule`: `acyclic`) A blocker would create a cycle. `ids`: every offending blocker, ascending; `cycles[i]`: the shortest cycle through `ids[i]` (ties by smallest ID sequence), starting at `id`. |

**Warnings:**

| Kind | When |
|---|---|
| `duplicate-id` | A blocker ID has more than one task file. |

**Partial schema:** none. `block` replaces a single file, all-or-nothing.

**Crash behavior:** the task file is either the old version or the new one. There is nothing for `doctor` to find beyond a possible leftover temp file.

**Retry safety:** safe. Blockers already present are no-ops and are not rechecked, so rerunning adds nothing new and returns `added: []` — whatever has happened to the tree since. After a `conflict` (`acyclic`), drop the offending blockers and rerun the rest.

### unblock

Remove one or more blockers from a task's `blocked_by`. Removing an ID that isn't there changes nothing.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "unblock-input",
  "type": "object",
  "required": ["id", "blockers"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id", "description": "The task to unblock." },
    "blockers": { "type": "array", "minItems": 1, "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "IDs to remove from the task's blocked_by." }
  },
  "additionalProperties": false
}
```

**Additional validation:** none. An ID in `blockers` that isn't in the task's `blocked_by` — including `id` itself — is simply not there to remove.

**Preconditions:** exactly one task file has ID `id`. The task may be open or complete. The blockers need not exist: removing an ID that names no task is how a dangling reference is cleared.

**Needed files:** the task file(s) with ID `id`. There is no index, so `unblock` walks the whole tree to find it — which also finds duplicates — and fails with `io` if it meets a folder it can't list. It reads no other task file: removing an edge cannot create a cycle, and the blockers' existence doesn't matter. No relevant files.

**Effects:**

- The task's `blocked_by` becomes `blocked_by` − `blockers`.
- Every other field, the task file's `schema`, and the `.md` are unchanged.

The task may become ready; readiness is derived when read (see [Dependencies](design-spec.md#dependencies)), so no other file is rewritten.

**Invariants at risk:** none. Removing edges cannot create a cycle or a dangling reference.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "unblock-output",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "blocked_by", "tags", "extra", "folder", "notes_path", "removed"],
  "properties": {
    "schema": { "$ref": "task#/properties/schema" },
    "id": { "$ref": "task#/properties/id" },
    "title": { "$ref": "task#/properties/title" },
    "priority": { "$ref": "task#/properties/priority" },
    "created_at": { "$ref": "task#/properties/created_at" },
    "completed_at": { "$ref": "task#/properties/completed_at" },
    "blocked_by": { "$ref": "task#/properties/blocked_by" },
    "tags": { "$ref": "task#/properties/tags" },
    "extra": { "$ref": "task#/properties/extra" },
    "folder": { "$ref": "task#/properties/folder" },
    "notes_path": { "$ref": "task#/properties/notes_path" },
    "removed": { "type": "array", "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "The blockers this operation removed, in ascending order; empty if none were present." }
  },
  "additionalProperties": false
}
```

The task after the operation, per the [Task](#task) schema, plus `removed`.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `id` or `blockers` is missing or invalid, or `blockers` is empty or repeats an ID. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found` | No task file has ID `id` (`ids`: `[id]`). |
| `corrupt`, `unsupported-format` | The task file is unusable. |
| `conflict` | (`rule`: `duplicate-id`) More than one task file has ID `id` (`ids`: `[id]`). A write must know which task it changes; repair the duplicate with [`doctor`](design-spec.md#doctor) first. |

**Warnings:** none.

**Partial schema:** none. `unblock` replaces a single file, all-or-nothing.

**Crash behavior:** the task file is either the old version or the new one. There is nothing for `doctor` to find beyond a possible leftover temp file.

**Retry safety:** safe. Blockers already absent are left absent, so rerunning removes nothing more and returns `removed: []`.

### update

Change one or more of a task's user-owned fields: `title`, `priority`, `tags`, `extra`. Fields not named in the input are left unchanged.

Other fields have their own operations: `blocked_by` ([`block`](#block), [`unblock`](#unblock)), `completed_at` ([`complete`](#complete), [`reopen`](#reopen)), the folder ([`move`](#move)). Notes are edited directly in the `.md`. `id`, `schema`, and `created_at` never change.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "update-input",
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id", "description": "The task to update." },
    "title": { "type": "string", "description": "Replace the title. Untrimmed; trimmed and validated per Titles (see Additional validation)." },
    "priority": { "$ref": "task-file#/properties/priority", "description": "Set the priority; null clears it." },
    "tags": {
      "oneOf": [
        {
          "type": "object",
          "required": ["replace_all"],
          "properties": { "replace_all": { "$ref": "task-file#/properties/tags", "description": "The complete new tag set; [] clears it." } },
          "additionalProperties": false
        },
        {
          "type": "object",
          "minProperties": 1,
          "properties": {
            "add": { "$ref": "#/$defs/tag-list", "description": "Tags to add." },
            "remove": { "$ref": "#/$defs/tag-list", "description": "Tags to remove." }
          },
          "additionalProperties": false
        }
      ]
    },
    "extra": {
      "oneOf": [
        {
          "type": "object",
          "required": ["replace_all"],
          "properties": { "replace_all": { "$ref": "task-file#/properties/extra", "description": "The complete new extra map; {} clears it." } },
          "additionalProperties": false
        },
        {
          "type": "object",
          "minProperties": 1,
          "properties": {
            "merge": { "type": "object", "minProperties": 1, "description": "Keys to set; each replaces that key's whole value (shallow merge)." },
            "remove": { "type": "array", "minItems": 1, "uniqueItems": true, "items": { "type": "string" }, "description": "Keys to delete." }
          },
          "additionalProperties": false
        }
      ]
    }
  },
  "additionalProperties": false,
  "anyOf": [
    { "required": ["title"] },
    { "required": ["priority"] },
    { "required": ["tags"] },
    { "required": ["extra"] }
  ],
  "$defs": {
    "tag-list": { "type": "array", "minItems": 1, "uniqueItems": true, "items": { "$ref": "task-file#/$defs/name" } }
  }
}
```

**Additional validation:**

- `title` is trimmed, then validated, per [Titles](design-spec.md#titles).
- `tags.add` and `tags.remove` must not share a tag; `extra.merge` and `extra.remove` must not share a key.

The schema itself enforces the rest: at least one field to change (`{"id": 42}` alone is `invalid-input`); `replace_all` standing alone; and no empty `tags`/`extra` object or empty `add`, `remove`, or `merge`. With these rules, the order in which `add`/`remove` (or `merge`/`remove`) apply doesn't matter.

**Preconditions:** exactly one task file has ID `id`. The task may be open or complete.

**Needed files:** the task file(s) with ID `id`. `update` walks the whole tree to find it — which also finds duplicates — and fails with `io` if it meets a folder it can't list. It reads no other task file. No relevant files.

**Effects:** only the named fields change:

- **`title`** — replaced by the trimmed title.
- **`priority`** — set to the given integer, or cleared by `null`. Absent means unchanged.
- **`tags`** — `replace_all`: becomes exactly the given set. `add`/`remove`: `tags` ∪ `add` − `remove`. Adding a tag already present, or removing one that isn't, changes nothing.
- **`extra`** — `replace_all`: becomes exactly the given map. `merge`: each given key is set to the given value, replacing any earlier value wholesale (no recursive merge into objects); `null` is an ordinary value, not a deletion. `remove`: each given key is deleted; a key that isn't present changes nothing. Key order follows [File format](design-spec.md#file-format): existing keys keep their position; new keys are appended in the order given.

Every other field, the task file's `schema`, and the `.md` are unchanged.

A field counts as **changed** when its new value differs from its old one *as a JSON value*: maps compare regardless of key order, numbers by numeric value, `tags` as a set. If no field changes by that comparison, the task file is **not rewritten** at all — a no-op `update` leaves the file, and its layout, untouched.

**Invariants at risk:** none. None of these fields takes part in an invariant.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "update-output",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "blocked_by", "tags", "extra", "folder", "notes_path", "changed"],
  "properties": {
    "schema": { "$ref": "task#/properties/schema" },
    "id": { "$ref": "task#/properties/id" },
    "title": { "$ref": "task#/properties/title" },
    "priority": { "$ref": "task#/properties/priority" },
    "created_at": { "$ref": "task#/properties/created_at" },
    "completed_at": { "$ref": "task#/properties/completed_at" },
    "blocked_by": { "$ref": "task#/properties/blocked_by" },
    "tags": { "$ref": "task#/properties/tags" },
    "extra": { "$ref": "task#/properties/extra" },
    "folder": { "$ref": "task#/properties/folder" },
    "notes_path": { "$ref": "task#/properties/notes_path" },
    "changed": {
      "type": "array",
      "uniqueItems": true,
      "items": { "type": "string", "enum": ["title", "priority", "tags", "extra"] },
      "description": "The fields whose value changed, compared as JSON values (see Effects), in the order title, priority, tags, extra; empty if none did, in which case the file was not rewritten."
    }
  },
  "additionalProperties": false
}
```

The task after the operation, per the [Task](#task) schema, plus `changed`.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | Any input fails validation — including no field to change, `replace_all` combined with another form, or `add`/`remove` (`merge`/`remove`) sharing an entry. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found` | No task file has ID `id` (`ids`: `[id]`). |
| `corrupt`, `unsupported-format` | The task file is unusable. |
| `conflict` | (`rule`: `duplicate-id`) More than one task file has ID `id` (`ids`: `[id]`). A write must know which task it changes; repair the duplicate with [`doctor`](design-spec.md#doctor) first. |

**Warnings:** none.

**Partial schema:** none. `update` replaces a single file, all-or-nothing.

**Crash behavior:** the task file is either the old version or the new one. There is nothing for `doctor` to find beyond a possible leftover temp file.

**Retry safety:** safe. Every form is idempotent: rerunning with the same input leaves the task as it is and returns `changed: []`.

### delete

Permanently remove a task, and remove its ID from every other task's `blocked_by`. ftask keeps no copy; see [Undo](#undo).

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "delete-input",
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id" }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:**

- Exactly one task file has ID `id`. The task may be open or complete, and its task file may be unusable: `delete` needs only its filename, never its contents — so it is also how an unusable task is removed.
- `id` is at most `last_id`: a task above it only arises from a system crash or an outside change, and removing it would let its ID be reissued undetectably (see [Task IDs](design-spec.md#task-ids)).

**Needed files:** every task file whose filename ID is `id`, and every task file whose `blocked_by` contains `id` — those are rewritten. There is no index, so `delete` walks the whole tree, and fails with `io` if it meets a folder it can't list: it must find every reference. Relevant files: every other task file — one that is unusable may hold a reference that can't be removed, so it is a warning, and the reference is left for [`doctor`](design-spec.md#doctor).

**Effects:**

- Every task file whose `blocked_by` contains `id` has it removed. Every other field, the task file's `schema`, and the `.md` are unchanged.
- The task no longer exists, nor does its `.md`.

Tasks that were blocked only by this one become ready, as they would if it had been completed: deleting a task says its work won't happen, like cancelling it.

**Invariants at risk:**

- *No dangling references* — every reference to `id` is removed, under the write lock, before the task is.
- *Unique IDs*, *IDs within `last_id`* — removing a task cannot break either; the `last_id` precondition keeps `id` from being reissued.
- *Acyclic* — cannot be broken: removing edges and a task cannot create a cycle.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "delete-output",
  "type": "object",
  "required": ["id", "folder", "dependents"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id", "description": "The task deleted." },
    "folder": { "$ref": "folder-path", "description": "The folder it was in." },
    "dependents": { "type": "array", "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "Tasks whose blocked_by contained id, in ascending order; each had it removed." }
  },
  "additionalProperties": false
}
```

`id` and `folder` locate the task in git; `dependents` is what to re-`block` after restoring it (see [Undo](#undo)).

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `id` is missing or not a valid task ID. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found` | No task file has ID `id` (`ids`: `[id]`). |
| `conflict` | (`rule`: `duplicate-id`) More than one task file has ID `id` (`ids`: `[id]`). A write must know which task it removes; repair the duplicate with [`doctor`](design-spec.md#doctor) first. |
| `conflict` | (`rule`: `id-above-last-id`) `id` is above `last_id` (`ids`: `[id]`). Repair with `doctor` first. |

The `conflict` rules are checked in the order listed; the first that applies is reported.

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | Another task file is unusable, so any reference it holds to `id` can't be removed. |

Every unusable task file in the tree is reported, not only those known to reference `id`: an unusable file's `blocked_by` can't be read, so any of them may be one, and each bears on whether the delete left a dangling reference.

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "delete-partial",
  "type": "object",
  "required": ["dependents"],
  "properties": {
    "dependents": { "type": "array", "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "Tasks whose reference to id was removed before the failure, in ascending order. The task was not removed." }
  },
  "additionalProperties": false
}
```

Present only when an error (e.g. `io`) comes after at least one dependent was rewritten. The task, with its notes, still exists; the rewritten dependents stay rewritten. Removing a reference to a task that still exists breaks no invariant, so the tree is valid. Once the task file is removed, `delete` cannot fail: removing the `.md` afterwards is cleanup, and if it fails the orphaned `.md` is left for `doctor`, with no warning.

**Crash behavior:** steps run in this order, so a [process crash](design-spec.md#crashes) leaves a valid tree:

1. Each dependent is rewritten, one file at a time. A crash here leaves some references removed and the task in place.
2. The task file is removed. This is the moment the task is gone. Removing it before the `.md` means a failure never leaves a surviving task without its notes.
3. The `.md` is removed. A crash before this leaves an orphaned `.md`, which reads ignore and `doctor` reports.

After a system crash the ordering may not survive: the removal can persist while a dependent's rewrite is lost, leaving a dangling reference for `doctor`.

**Retry safety:** after `busy`, an error with `partial`, or a crash before step 2, safe: rerunning removes what is left, and reports only the dependents it rewrote itself. After step 2, rerunning fails with `not-found`; an orphaned `.md` is left for `doctor`.

### move

Move a task into a folder. Its ID, fields, and notes go with it; moving a task to the folder it is already in changes nothing.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "move-input",
  "type": "object",
  "required": ["id", "to"],
  "properties": {
    "id": { "$ref": "task-file#/properties/id" },
    "to": { "$ref": "folder-path", "description": "The folder to move the task into." },
    "parents": { "type": "boolean", "default": false, "description": "Create to, and any missing folders above it, instead of failing." }
  },
  "additionalProperties": false
}
```

**Additional validation:** none.

**Preconditions:** exactly one task file has ID `id`; the task may be open or complete. `to` exists, or `parents` is true, as for [`create-folder`](#create-folder):

| State of `to` | Outcome |
|---|---|
| The [path walk](#path-walk) finds every entry, including `to`, a plain directory | The task moves into `to` — or, if it is already there, nothing changes. |
| It stops at a missing folder, `parents` false | `not-found` (`folders`: the outermost missing folder). |
| It stops at a missing folder, `parents` true | That folder and every folder below it on the path, `to` included, are created, outermost first; the task moves into `to`. |
| It stops at an entry that is not a directory, or is a symlink | `corrupt` (`reason`: `unexpected-file`). |

**Needed files:** the entries along `to`'s path ([path walk](#path-walk)), and every task file whose filename ID is `id`. There is no index, so `move` walks the whole tree to find it — which also finds duplicates — and fails with `io` if it meets a folder it can't list. No relevant files beyond that: a task's identity is its ID, not its location, so neither its blockers nor its dependents are read.

**Effects:**

- The task is in `to`, with every field, its task file's `schema`, and its `.md` unchanged. A stray `.md` already in `to` under the task's name — left by an editor after an earlier move (see [Assumptions](design-spec.md#assumptions)) — is replaced by the task's notes, or removed if the task has none.
- With `parents`, `to` and every folder above it exist.
- If the task was already in `to`: nothing changes.

**Invariants at risk:** none. `blocked_by` names tasks by ID, so moving one changes no reference.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "move-output",
  "type": "object",
  "required": ["schema", "id", "title", "priority", "created_at", "completed_at", "blocked_by", "tags", "extra", "folder", "notes_path", "from", "created", "changed"],
  "properties": {
    "schema": { "$ref": "task#/properties/schema" },
    "id": { "$ref": "task#/properties/id" },
    "title": { "$ref": "task#/properties/title" },
    "priority": { "$ref": "task#/properties/priority" },
    "created_at": { "$ref": "task#/properties/created_at" },
    "completed_at": { "$ref": "task#/properties/completed_at" },
    "blocked_by": { "$ref": "task#/properties/blocked_by" },
    "tags": { "$ref": "task#/properties/tags" },
    "extra": { "$ref": "task#/properties/extra" },
    "folder": { "$ref": "task#/properties/folder" },
    "notes_path": { "$ref": "task#/properties/notes_path" },
    "from": { "$ref": "folder-path", "description": "The folder the task was in before the operation." },
    "created": { "type": "array", "items": { "$ref": "folder-path" }, "description": "Folders created (parents), outermost first; empty if none." },
    "changed": { "type": "boolean", "description": "True if the task moved; false if it was already in to." }
  },
  "additionalProperties": false
}
```

The task after the operation, per the [Task](#task) schema, plus `from`, `created`, and `changed`.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `id` or `to` is missing or invalid. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found`, `corrupt` | `to` fails the [path walk](#path-walk) while `parents` is false (`not-found`, or `corrupt` with `reason` `unexpected-file`); or no task file has ID `id` (`not-found`). A missing folder and a missing task are reported in one `not-found`. |
| `corrupt`, `unsupported-format` | The task file is unusable. |
| `conflict` | (`rule`: `duplicate-id`) More than one task file has ID `id` (`ids`: `[id]`). A write must know which task it moves; repair the duplicate with [`doctor`](design-spec.md#doctor) first. |

**Warnings:** none.

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "move-partial",
  "type": "object",
  "required": ["created"],
  "properties": {
    "created": { "type": "array", "items": { "$ref": "folder-path" }, "description": "Folders created before the failure, outermost first. The task did not move." }
  },
  "additionalProperties": false
}
```

Present only when an error (e.g. `io`) comes after `parents` created at least one folder. The created folders stay. Once the task file has moved, `move` cannot fail: removing the old `.md` afterwards is cleanup, and if it fails the stray `.md` is left for `doctor`, with no warning.

**Crash behavior:** a task is two files, which can't move in one step. Moving each with a plain `rename` would make the notes look lost if a crash fell between the two, whichever went first. So the notes are copied before they are removed:

1. With `parents`, missing folders are created, outermost first.
2. The `.md` is hard-linked into `to`, replacing any stray `.md` there. A [process crash](design-spec.md#crashes) here leaves the task in place with its notes, and a stray `.md` in `to` that a retry replaces.
3. The task file is renamed into `to`. This is the moment the task moves; its notes are already there.
4. The old `.md` is removed. A crash before this leaves a stray `.md` in the old folder, which reads ignore and `doctor` reports.

A task with no `.md` skips steps 2 and 4, and instead removes any stray `.md` in `to` under its name before step 3, so a stray can never become its notes. The notes are never lost, and the tree always satisfies every invariant.

**Retry safety:** safe. After `busy`, an error with `partial`, or a crash, rerunning completes the move. After success, rerunning finds the task already in `to`, changes nothing, and returns `changed: false`.

### frontier

Return the ready tasks — open, and not blocked — in the order to work on them.

**Kind:** read. Takes no lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "frontier-input",
  "type": "object",
  "properties": {
    "folder": { "$ref": "folder-path", "default": "/", "description": "Only tasks in this folder are returned." },
    "recursive": { "type": "boolean", "default": true, "description": "Also return tasks in the folder's subfolders." }
  },
  "additionalProperties": false
}
```

With no input, `frontier` returns every ready task in the tree.

**Additional validation:** none.

**Preconditions:** `folder` exists.

**Needed files:** the entries along `folder`'s path, checked by the [path walk](#path-walk) — and no task files: one bad task file must not make the whole frontier fail, so every task-file problem is a warning. `frontier` walks the whole tree, since blockers may be in any folder; a folder it can't list is an `unreadable-folder` warning. It reads the contents of every task file in scope, and of the blockers' task files of every open task in scope. Relevant files: those same task files.

`folder` and `recursive` limit only which tasks are *returned*. Blockers are looked up anywhere in the tree, since folders play no part in dependencies: a task in `/proj` blocked by an open task in `/infra` is not on `/proj`'s frontier.

**Effects:** none.

**Invariants at risk:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "frontier-output",
  "type": "object",
  "required": ["tasks"],
  "properties": {
    "tasks": {
      "type": "array",
      "items": { "$ref": "task-view" },
      "description": "Every ready task in scope, in frontier order. May be empty."
    }
  },
  "additionalProperties": false
}
```

Each item is a [Task view](#task-view); for `frontier`, `readiness` is always `ready` and `blocking` always empty. The shape is shared with `show` so that callers parse one form.

**Order:**

1. `priority`, highest first; tasks with no priority after all prioritized tasks.
2. Then `id`, lowest first — the oldest first, since IDs only increase.
3. Copies of a duplicated ID in [tree order](#tree-order).

Narrowing the result further — by tag, by `extra` fields, to the first N — is left to the caller (e.g. `jq`) or the CLI; it does not change the order (see [Conventions](#conventions)).

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `folder` is not a valid folder path, or `recursive` is not a boolean. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `not-found`, `corrupt` | `folder` fails the [path walk](#path-walk) (`not-found`, or `corrupt` with `reason` `unexpected-file`). |

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A task file in scope is unusable, so the task may be missing from the frontier; or a blocker's task file of an open task in scope is unusable, so that task counts as blocked. |
| `duplicate-id` | An ID has more than one task file in scope (each copy is judged separately, and every ready copy is returned); or a blocker's ID of an open task in scope does, so that task counts as blocked. |
| `dangling-reference` | An open task in scope has a `blocked_by` ID with no task file, so it counts as blocked. |
| `unreadable-folder` | A folder could not be listed; tasks in it are missing from the frontier, and tasks blocked by tasks in it count as blocked. |

**Partial schema:** none.

**Crash behavior:** none. `frontier` changes nothing.

**Retry safety:** safe. The result reflects the tree at the time of the read; with no reservation, two callers may be handed the same task (see [Non-guarantees](design-spec.md#non-guarantees)).

### list

Return every task in scope, whatever its readiness, with its readiness shown — and optionally the folders in scope. Complete tasks are included only on request.

**Kind:** read. Takes no lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "list-input",
  "type": "object",
  "properties": {
    "folder": { "$ref": "folder-path", "default": "/", "description": "Only tasks (and folders) in this folder are returned." },
    "recursive": { "type": "boolean", "default": true, "description": "Also return tasks (and folders) in the folder's subfolders." },
    "include_complete": { "type": "boolean", "default": false, "description": "Also return complete tasks." },
    "include_folders": { "type": "boolean", "default": false, "description": "Also return the folders in scope: with recursive, every folder under folder; without, folder and its immediate subfolders." }
  },
  "additionalProperties": false
}
```

With no input, `list` returns every open task in the tree.

Notes: `include_complete` and `include_folders` change what the result *is* — "open tasks" versus "all tasks", "tasks" versus "tasks and folders" — so they are parameters rather than caller-side filtering (see [Conventions](#conventions)). Complete tasks accumulate forever; excluding them by default keeps the ordinary result about current work.

**Additional validation:** none.

**Preconditions:** `folder` exists.

**Needed files:** as for [`frontier`](#frontier): the entries along `folder`'s path ([path walk](#path-walk)), and no task files. `list` walks the whole tree (a folder it can't list is an `unreadable-folder` warning), and reads the contents of every task file in scope and of the blockers' task files of every open task in scope, since readiness needs them. Relevant files: those same task files.

**Effects:** none.

**Invariants at risk:** none.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "list-output",
  "type": "object",
  "required": ["tasks"],
  "properties": {
    "folders": {
      "type": "array",
      "items": { "$ref": "folder-path" },
      "description": "Present only when include_folders is true: folder itself, then — with recursive — every folder under it, or — without — its immediate subfolders; empty folders included; in tree order."
    },
    "tasks": {
      "type": "array",
      "items": { "$ref": "task-view" },
      "description": "Every task in scope — open ones, plus complete ones if include_complete — in tree order. May be empty."
    }
  },
  "additionalProperties": false
}
```

Each task is a [Task view](#task-view), the same shape `show` and `frontier` return.

**Order:** [tree order](#tree-order), for both `folders` and `tasks`.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | `folder` is not a valid folder path, or a flag is not a boolean. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format` | The config can't be located, or the root is not usable (see [Root states](#root-states)). |
| `not-found`, `corrupt` | `folder` fails the [path walk](#path-walk) (`not-found`, or `corrupt` with `reason` `unexpected-file`). |

**Warnings:**

| Kind | When |
|---|---|
| `unusable-file` | A task file in scope is unusable, so the task may be missing from the list; or a blocker's task file of an open task in scope is unusable, so that task counts as blocked. |
| `duplicate-id` | An ID has more than one task file in scope (every copy in scope is listed); or a blocker's ID of an open task in scope does, so that task counts as blocked. |
| `dangling-reference` | An open task in scope has a `blocked_by` ID with no task file, so it counts as blocked. |
| `unreadable-folder` | A folder could not be listed; tasks in it are missing from the list, and tasks blocked by tasks in it count as blocked. |

**Partial schema:** none.

**Crash behavior:** none. `list` changes nothing.

**Retry safety:** safe.

## Planned operations

Operations not yet specified, with the constraints already decided.

### migrate

Upgrade a tree from one format version to the next — `ftask.json` and every task file — as a single explicit operation (see [Format versions](design-spec.md#format-versions)). Until then, a binary that supports a different format than the tree's cannot use it.
