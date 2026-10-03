# ftask create-batch spec

`create-batch`: create several tasks in one call, with dependencies between them named by local references instead of IDs that don't exist yet. It is how an agent writes down a plan: one call, under one write lock, checked as a whole before anything is written.

This document specifies one new [operation](operations.md) and the CLI command that runs it, each in its own spec's template. When they are implemented, the operation moves into [operations.md](operations.md) after [`create`](operations.md#create) and the command into the [CLI spec](cli-spec.md) after [`create`](cli-spec.md#create) (see [Shipping](#shipping)).

## Goals

- **One call for a plan.** Ten tasks with dependencies among them take one call, not ten `create`s with each new ID threaded into the next `--blocked-by` by hand.
- **Checked as a whole.** Every task, folder, blocker and reference is checked before anything is written. A plan with a mistake in it creates nothing.
- **Structure included.** A plan's folders are created with it, so a plan for a new project is one call too.
- **Small output.** The caller wrote the tasks, so it gets back their IDs, not the tasks.

## Non-goals

- **Changing existing tasks.** Every task in the batch is new. Making an existing task wait on new ones (e.g. breaking task 41 down into steps) is a [`block`](operations.md#block) after the batch: `ftask block 41 --blockers <ids>`. Doing it inside the batch would make it rewrite existing task files and run a cycle check; see [Future work](#future-work).
- **Atomicity against failures partway through.** All checks come before any write, so anything ftask can foresee fails cleanly. A filesystem error or a crash partway through the writes leaves the folders and tasks before it created; see the operation's Partial schema and Crash behavior. The filesystem gives no way to publish several files as one step.

## References

A task in the batch may have a **`ref`**: a name, local to the batch, that later tasks in the batch use in their `blocked_by` to wait on it. A `blocked_by` item is either an **integer**, the ID of an existing task, or a **string**, the `ref` of an earlier task in the batch.

- **Earlier only.** A ref names a task that comes *before* the referring one in `tasks`. Write blockers first, then what they block. This rules out cycles within the batch without a cycle check, and means the tasks can be written in input order with every blocker already in place (see the operation's Crash behavior). A ref to a later task is `invalid-input`, as is a ref to the task itself.
- **Names.** A `ref` follows the [tag](design-spec.md#tags) name rule (lowercase letters, digits and inner hyphens, 1 to 64 characters), and is unique within the batch. `ref` is optional: a task nothing in the batch waits on needs none.
- **Never stored.** Refs exist only in the input and the output. The tasks created are ordinary tasks, and a ref means nothing in a later call.

Why strings and integers in one list, rather than a prefix like `@schema`: JSON already tells them apart, so a ref needs no escaping and an ID needs no quoting.

## Folders

Every folder a task names is created if it is missing, along with any missing folders above it, as [`create-folder`](operations.md#create-folder) with `parents` does. There is no option to turn this off: a batch is written with thought given to its structure, and a folder it names is one it means to have.

- **A typo makes a folder.** `/work/apii` is created rather than refused. The output's `folders_created` lists every folder the batch created, so a stray one shows up in the result; [`delete-folder`](operations.md#delete-folder) removes it with its tasks, or [`move`](operations.md#move) the tasks out first.
- **Never through a symlink or a file.** An entry on a folder's path that is not a plain directory fails the batch before anything is written, as the [path walk](operations.md#path-walk) does everywhere.

## Operation

### create-batch

Create one or more new, open tasks, in order, and any folders they need, with dependencies on existing tasks and on earlier tasks in the batch. All-or-nothing as far as checks go: every task is checked before any is written, and if any check fails, nothing changes.

**Kind:** write. Takes the write lock. Requires a usable root.

**Input schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "create-batch-input",
  "type": "object",
  "required": ["tasks"],
  "properties": {
    "folder": { "$ref": "folder-path", "default": "/", "description": "The folder of every task that names none. Created if missing, as every task's folder is." },
    "tasks": {
      "type": "array",
      "minItems": 1,
      "maxItems": 1000,
      "items": {
        "type": "object",
        "required": ["title"],
        "properties": {
          "ref": { "$ref": "task-file#/$defs/name", "description": "Local name for this task, for later tasks' blocked_by. Unique within the batch." },
          "title": { "type": "string", "description": "Untrimmed; trimmed and validated per Titles (see Additional validation)." },
          "folder": { "$ref": "folder-path", "description": "Defaults to the batch's folder. Created if missing, with any missing folders above it." },
          "priority": { "$ref": "task-file#/properties/priority", "default": null },
          "tags": { "$ref": "task-file#/properties/tags", "default": [] },
          "blocked_by": {
            "type": "array",
            "uniqueItems": true,
            "default": [],
            "items": {
              "oneOf": [
                { "$ref": "task-file#/properties/id" },
                { "$ref": "task-file#/$defs/name" }
              ]
            },
            "description": "IDs of existing tasks, and refs of earlier tasks in the batch."
          },
          "extra": { "$ref": "task-file#/properties/extra", "default": {} },
          "notes": { "type": "string", "default": "", "description": "Initial notes, written to the task's .md." }
        },
        "additionalProperties": false
      }
    }
  },
  "additionalProperties": false
}
```

**Additional validation:**

- Each `title` is trimmed, then validated, per [Titles](design-spec.md#titles), as in [`create`](operations.md#create).
- Each `ref` is unique within `tasks` (`field`: the later one's `/tasks/<i>/ref`).
- Each string in a `blocked_by` is the `ref` of an earlier task in `tasks` (`field`: `/tasks/<i>/blocked_by/<j>`). A ref that names a later task, the task itself, or no task in the batch is `invalid-input`; the `reason` says which.
- `id`, `schema`, `created_at`, `completed_at`, and `updated_at` are never input, as in `create`.

Like every `invalid-input`, every problem in every task is reported, sorted by `field`, the first 20 listed.

**Preconditions:** every task's folder either exists or can be created: the [path walk](operations.md#path-walk) meets no entry that is not a plain directory, or is a symlink. Every integer in any `blocked_by` names an existing task, open or complete. An ID with several task files exists. `last_id` plus the number of tasks is within the [ID ceiling](design-spec.md#task-ids).

**Needed files:** the entries along each distinct folder's path ([path walk](operations.md#path-walk)), up to the first missing one, and the task files whose filename ID is an integer in any `blocked_by`. When any `blocked_by` holds an integer, `create-batch` walks the whole tree once to look them up, and fails with `io` if it meets a folder it can't list; otherwise it does not walk the tree. Relevant files: the same task files — an ID with more than one task file is a warning, not an error.

**Effects:**

- Every task's folder exists, with every folder above it. Those that were missing are created.
- `last_id` is raised by the number of tasks, *n*, in one write. The tasks' IDs are the *n* IDs above the old `last_id`, in input order: the first task gets old `last_id` + 1. They are consecutive, since the write lock excludes other writes.
- Each task exists in its folder with the given fields, as [`create`](operations.md#create) would create it, except that each string in its `blocked_by` is replaced by the ID of the task with that `ref`. `blocked_by` is stored as a set of IDs, as always.
- Every task has the same `created_at` and `updated_at`: the time the batch started writing.
- Each task's `.md` exists, containing its `notes`, as for `create`, including the replacement of a stray `.md`. If writing one fails, `create-batch` still succeeds, with a `notes-missing` warning for that task.

**Invariants at risk:**

- *Unique IDs* and *IDs within `last_id`* — the IDs are allocated from `last_id` under the write lock, all at once, as in `create`; the same exception for a task above `last_id` applies (see Crash behavior).
- *No dangling references* — integers in `blocked_by` are checked against the tree under the write lock; refs are checked against the batch before anything is written, and resolve to tasks written earlier.
- *Acyclic* — cannot be broken. No existing task depends on a new one, and within the batch a task waits only on earlier tasks, so every new edge points to an older task.

Folders carry no invariants.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "create-batch-output",
  "type": "object",
  "required": ["ids", "refs", "folders_created"],
  "properties": {
    "ids": {
      "type": "array",
      "minItems": 1,
      "items": { "$ref": "task-file#/properties/id" },
      "description": "The created tasks' IDs, in input order: ids[i] is tasks[i]'s."
    },
    "refs": {
      "type": "object",
      "propertyNames": { "$ref": "task-file#/$defs/name" },
      "additionalProperties": { "$ref": "task-file#/properties/id" },
      "description": "Each ref given, and the ID of the task it named. Empty when no task had a ref."
    },
    "folders_created": {
      "type": "array",
      "items": { "$ref": "folder-path" },
      "description": "The folders this operation created, in tree order. Empty when every folder existed."
    }
  },
  "additionalProperties": false
}
```

The tasks themselves are not returned: the caller wrote them, and the result of an agent's call lands in its context (see the [Parameters](operations.md#conventions) convention). [`show`](operations.md#show) returns any of them whole, `notes_path` included.

**Order:** `ids` is in input order; `folders_created` in [tree order](operations.md#tree-order), so a parent comes before its children.

**Errors:**

| Kind | When |
|---|---|
| `invalid-input` | Any input fails validation: a bad title, tag, folder path, or type; a duplicate `ref`; a string in `blocked_by` that names no earlier task in the batch; more than 1,000 tasks, or none. Every problem in every task is reported. |
| `environment`, `not-initialized`, `corrupt`, `unsupported-format` | The config can't be located, or the root is not usable (see [Root states](operations.md#root-states)). |
| `busy` | Another write holds the write lock. |
| `not-found`, `corrupt` | An entry on a folder's path is not a plain directory, or is a symlink (`corrupt`, `reason`: `unexpected-file`); or an integer in a `blocked_by` has no task file (`not-found`, `ids`: every missing one, across all tasks). A missing folder is never an error: it is created. |
| `corrupt`, `unsupported-format` | A needed task file is corrupt or has an unsupported `schema`; or a new task's task file already exists in its folder (`corrupt`, `reason`: `unexpected-file`, with a `partial`). |
| `conflict` | (`rule`: `id-exhausted`) The last of the *n* IDs would exceed the ID ceiling. Checked before `last_id` is raised, so nothing is consumed. |

Every row but the `corrupt` for an existing task file is found before anything is written. That one, and `io`, can occur partway through the writes, and come with a `partial` when anything had been written.

**Warnings:**

| Kind | When |
|---|---|
| `notes-missing` | A task was created but its `.md` could not be written. One warning per such task. |
| `duplicate-id` | An integer in a `blocked_by` has more than one task file. One warning per ID, however many tasks name it. |

**Partial schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "create-batch-partial",
  "type": "object",
  "required": ["folders_created", "consumed", "ids", "refs"],
  "properties": {
    "folders_created": {
      "type": "array",
      "items": { "$ref": "folder-path" },
      "description": "The folders created before the failure, in tree order. They stay."
    },
    "consumed": {
      "type": "array",
      "items": { "$ref": "task-file#/properties/id" },
      "description": "Every ID raised from last_id for the batch, ascending: consumed[i] was tasks[i]'s. Empty when the failure came before last_id was raised."
    },
    "ids": {
      "type": "array",
      "items": { "$ref": "task-file#/properties/id" },
      "description": "The tasks created before the failure: the first len(ids) tasks, in input order. May be empty."
    },
    "refs": {
      "type": "object",
      "propertyNames": { "$ref": "task-file#/$defs/name" },
      "additionalProperties": { "$ref": "task-file#/properties/id" },
      "description": "The refs of the tasks in ids, and their IDs."
    }
  },
  "additionalProperties": false
}
```

Present only when the failure came after something was written: a folder created, or `last_id` raised. The tasks are written in input order, and a failure stops the batch, so the tasks created are always a prefix of `tasks`: `ids` are tasks 0 to *k*−1, and tasks *k* onwards were not created; their IDs, the rest of `consumed`, are allowed gaps. As in `create`, once a task's file is written that task is created: a failure writing its `.md` is a `notes-missing` warning, not a failure.

**Crash behavior:** after a [process crash](design-spec.md#crashes), the tree satisfies every invariant, leaving nothing for [`doctor`](operations.md#doctor) beyond a possible `temp-leftover`, because steps run in this order:

1. Missing folders are created, in tree order. A crash here leaves some of them created, and empty folders are valid.
2. `last_id` is raised by *n*. A crash here consumes *n* IDs without creating a task — allowed gaps.
3. For each task in input order: its task file is created, then its `.md`. A crash here leaves the tasks before it created, the one in progress either created (with its `.md` possibly missing, which reads as empty notes) or not, and the rest not. Each created task's blockers exist: refs name earlier tasks, which were written first.

A [system crash](design-spec.md#crashes) keeps the order of steps 2 and 3, since `ftask.json` and each task file are flushed before the next step. Creating a folder is not flushed (see [Crashes](design-spec.md#crashes)), so a system crash can lose a new folder and, with it, the tasks written into it: their IDs become gaps, and a task elsewhere that a lost one blocked is left with a `dangling-reference`, which `doctor` finds and [`repair`](operations.md#repair) removes. The other exception is the one [`create`](operations.md#create) describes: an outside change, or a disk that ignores flushes, can leave tasks with IDs above `last_id`, which the batch's IDs may then collide with. A collision in the same folder fails the batch at that task with `corrupt` and a `partial`; in a different folder it produces a `duplicate-id`. `doctor` finds both, as for `create`.

**Retry safety:** after `busy`, safe — nothing happened. After an error before the writes (every kind but those with a `partial`), safe — nothing changed. After an error with `partial`, rerunning the whole batch is safe only if `ids` is empty: folders already created are not an error, and the IDs are consumed afresh. Otherwise it is **not** safe: it creates the tasks in `ids` again, with new IDs. Rerun only the tasks not created — `tasks` from index `len(ids)` on — with each string in their `blocked_by` that names a created task replaced by its ID from `partial.refs`. After a crash or an unclear outcome, **not** safe, as for `create`: check which tasks exist first, e.g. by title with [`list`](operations.md#list). See [Idempotent create](design-spec.md#idempotent-create), which would cover a batch with one key.

## Command

### create-batch

Create several tasks in one call, with dependencies between them named by refs. Runs [`create-batch`](#create-batch).

**Synopsis:** `ftask create-batch -i <file>`.

**Operation:** [`create-batch`](#create-batch).

**Arguments:** none. The tasks are a list of objects, which only `--input` can carry.

**Options:** none beyond the [global options](cli-spec.md#global-options). `--input` is required; without it, the command is a [usage error](cli-spec.md#usage-errors).

**Input:** none beyond `--input`, taken as-is.

**Output:** Passthrough: `ids`, `refs` and `folders_created`.

**Errors:** none beyond the operation's.

**Examples:**

```sh
jq -n '{folder: "/work/api", tasks: [
  {ref: "schema", title: "Design schema", priority: 2, tags: ["db"]},
  {ref: "migrate", title: "Write migrations", blocked_by: ["schema"]},
  {ref: "backfill", title: "Backfill old rows", blocked_by: ["migrate"]},
  {title: "Deploy", blocked_by: ["migrate", "backfill", 12]}
]}' | ftask create-batch -i -                     # creates /work/api if missing; .result.refs.schema is Design schema's ID

ftask create-batch -i plan.json | jq -r '.result.ids | join(",")' \
  | xargs ftask block 41 --blockers               # task 41 now waits on the whole plan
```

## Shipping

Done with the implementation, not before, since they describe a command that exists:

- **operations.md** gets the operation after `create`, and **cli-spec.md** the command after `create`. This file is then deleted, as its content has moved.
- **The skill** learns to use `create-batch` when a request breaks down into several tasks with dependencies among them, instead of a chain of `create`s; and the recovery from a `partial` (rerun only the rest, with refs replaced).
- **The README's** taste gets a `create-batch` example.

## Future work

### Blocking existing tasks

A `blocks` list on a batch task, of existing IDs that should wait on it, so breaking task 41 down is one call. It would rewrite existing task files, which the batch otherwise never touches, and could close a cycle (a new task blocked by 41 and blocking it), so it needs [`block`](operations.md#block)'s cycle check and a crash behavior covering both kinds of file. A `block` after the batch does the same in a second call.
