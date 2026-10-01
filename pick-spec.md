# ftask pick spec

`ftask pick`: the interactive picker, built on [fzf](https://github.com/junegunn/fzf). Fuzzy-search tasks by title, act on them in place, and emit the ones chosen as JSON. It is the way a person works with ftask directly; agents use the other commands.

**Draft.** Not yet implemented.

`pick` is a CLI command, specified on top of the [CLI spec](cli-spec.md) and the [operations](operations.md). It runs no operation of its own. It composes [`list`](operations.md#list) for what it shows with the write operations its keys run, each as its own call. Everything the CLI spec says holds for `pick` except where this document says otherwise. Those places are collected in [Departures from the CLI spec](#departures-from-the-cli-spec).

## Goals

- **Fuzzy search by title** over the tree, with the task's details and notes in a preview.
- **Act without leaving.** Complete, edit, create, block, move, reprioritize and retag, then see the list reload.
- **A pipeline citizen.** Candidates can come from upstream (`ftask list … | ftask pick --from -`). The selection goes downstream as one [envelope](operations.md#output-envelope) (`ftask pick | jq …`). The interface draws on the terminal, never on stdin or stdout, so both can be redirected.
- **Nothing hidden from a caller.** Every change made inside the picker is reported in the output, so a script or an agent that hands the terminal to a person learns what the person changed.

## Non-goals

- **Agents.** `pick` is for a person at a terminal. Without one, it fails whenever it would show the picker (see [Errors](#errors)). The [ftask skill](claude/skills/ftask/SKILL.md) tells agents never to run it.
- **Deleting.** No key deletes a task or a folder. Deletes stay with `ftask delete` and `ftask delete-folder`, which agents' permission rules make ask first.
- **A configurable keymap.** The keymap is fixed and documented here. fzf's own options restyle the picker (see [fzf options](#fzf-options)).
- **A tree view.** Folders are a column and a narrowing step, not a nested display. That is the design spec's [Tree view](design-spec.md#tree-view).
- **Its own fuzzy matcher.** Matching, ranking and the screen are fzf's.

## Requirements

- **A terminal.** `/dev/tty` must open for reading and writing, whenever the picker is shown. stdin and stdout may be anything. A run that [selects at once](#selecting-at-once) shows nothing and needs no terminal.
- **fzf**, always, found on `PATH`, version 0.63.0 or later: the first release with the footer that holds the [status line](#status-line), the newest fzf feature `pick` uses. Its version is checked with `fzf --version` before fzf is started. The minimum is raised only deliberately, in a release that says so.
- **Optional:** `glow` or `bat` on `PATH` renders notes in the preview (see [Preview](#preview)).

## Command

Fuzzy-pick tasks, or with `--folders` folders, and write the selection as one envelope. Runs [`list`](operations.md#list) to load and reload the candidates, and the write operations of the [actions](#actions).

**Synopsis:** `ftask pick [--folder <path>] [--recursive=false] [--scope <scope>] [--tags-any <tags>] [--tags-all <tags>] [--ids <ids> | --from <file> | --source <command>] [--query <text>] [--select-one] [--exit-zero] [--fields <names>]`, `ftask pick --folders [--folder <path>] [--recursive=false] [--query <text>] [--select-one] [--exit-zero]`, or `ftask pick -i <file>`.

**Operation:** none of its own. Each load runs `list`, and each action runs one write operation per target task. No write lock is held between them, and none is held while the person looks at the list.

**Arguments:** none.

**Options:**

| Option | Field | Default |
|---|---|---|
| `--folder <path>` | `/folder` | `/`. An exact [folder path](cli-spec.md#command-line). The initial scope folder. The [`f` action](#actions) changes it during the session. |
| `--recursive` | `/recursive` | `true`. `--recursive=false` leaves out tasks, or folders, below `folder`. |
| `--scope <scope>` | `/scope` | `open`, or `all` with `--ids`, `--from` or `--source` (see [Candidates](#candidates)). Which tasks show at first: `ready`, `open` (ready and blocked), or `all` (complete too). The [`s` action](#actions) cycles it. |
| `--tags-any <tags>` | `/tags_any` | None. Comma list. As for [`list`](cli-spec.md#list). |
| `--tags-all <tags>` | `/tags_all` | None. Comma list. As for `list`. |
| `--ids <id,id,…>` | `/ids` | None. Only these tasks are candidates: a [snapshot](#candidates). |
| `--from <file>` | `/ids` | —. Reads an envelope from `<file>` (`-` is stdin) and takes its tasks' IDs as `ids` (see Input). |
| `--source <command>` | `/source` | None. A shell command whose output gives the candidates, run again on every reload: a [live source](#candidates). |
| `--query <text>` | `/query` | `""`. The initial search text. |
| `--select-one` | `/select_one` | `false`. If `query` matches exactly one candidate, emit it without showing the picker. |
| `--exit-zero` | `/exit_zero` | `false`. If `query` matches no candidate, emit an empty selection without showing the picker. |
| `--fields <names>` | `/fields` | None: whole task views. Comma list of task view field names, `id` always included. Shapes only the emitted `tasks`, as for `list`. |
| `--folders` | `/folders` | `false`. Pick folders instead of tasks: the [folder picker](#folder-picker). |

`--ids`, `--from` and `--source` are mutually exclusive. With `--folders`, only `--folder`, `--recursive`, `--query`, `--select-one` and `--exit-zero` apply. Any other field is `invalid-input`.

**Input:** pick's input schema, for `-i` and as the target of the options above:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "pick-input",
  "type": "object",
  "properties": {
    "folder": { "$ref": "folder-path", "default": "/" },
    "recursive": { "type": "boolean", "default": true },
    "scope": { "type": "string", "enum": ["ready", "open", "all"], "description": "Default open; all with ids or source." },
    "tags_any": { "$ref": "frontier-input#/properties/tags_any" },
    "tags_all": { "$ref": "frontier-input#/properties/tags_all" },
    "ids": { "type": "array", "uniqueItems": true, "items": { "$ref": "task-file#/properties/id" }, "description": "Only these tasks are candidates. May be empty." },
    "source": { "type": "string", "minLength": 1, "description": "A shell command whose output gives the candidates. Not with ids." },
    "query": { "type": "string", "default": "" },
    "select_one": { "type": "boolean", "default": false },
    "exit_zero": { "type": "boolean", "default": false },
    "fields": { "$ref": "frontier-input#/properties/fields" },
    "folders": { "type": "boolean", "default": false }
  },
  "additionalProperties": false,
  "not": { "required": ["ids", "source"] }
}
```

`--from` resolves to `ids`, as `create`'s `--notes-file` resolves to `notes`:

- <a id="accepted-envelopes"></a>**Accepted envelopes.** The file holds one envelope with `ok: true` whose `result` has either `tasks`, an array of objects each with an `id` (from `list`, `frontier` or `pick`), or an `id` of its own (from `show`, `create`, `complete` and the other single-task commands). Any `--fields` upstream will do, since `id` is always included. The IDs are taken in order, without duplicates.
- **Read in full first.** The whole file is read before fzf starts, so `--from -` never competes with the terminal.
- **Bad content** is `invalid-input` at `/ids`: not one JSON value, not an envelope, no tasks or ID in it, or an envelope with `ok: false`. The last says which error kind upstream reported. The upstream command has already written its own stderr line.
- **An empty `tasks` array** is fine. The picker opens with no candidates.

**Output:** one envelope, written after fzf has exited. See [Output](#output).

**Errors:** see [Errors](#errors).

**Examples:**

```sh
ftask pick                                                  # open tasks; Enter → .result.tasks
ftask pick --folder /work --scope ready                     # what's ready under /work
ftask pick | jq -r '.result.tasks[].id'                     # the IDs picked
ftask pick --fields id,title,notes_path | jq -r '.result.tasks[].notes_path' | xargs -r "$EDITOR"
ftask list --readiness blocked --fields id | ftask pick --from -   # choose among the blocked ones
ftask frontier --tags-any today | ftask pick --from -
ftask pick --source 'ftask frontier --tags-any today'       # the same, kept live
ftask pick --source "ftask list | jq -c '.result.tasks |= map(select(.extra.status == \"waiting\"))'"
ftask pick --ids 41,42,43
ftask complete "$(ftask pick --query 'renew pass' --select-one | jq -r '.result.tasks[0].id')"   # no picker if only one matches
ftask pick --folders | jq -r '.result.folders[0]'           # a folder path, e.g. for create --folder
ftask pick > picked.json; jq '.result.actions' picked.json  # what the session changed
ftask pick | ftask pick --from -                            # narrow in two passes
```

## Candidates

Which tasks the picker lists, its **candidates**, comes from one of three places. They differ in whether the list's membership can change during the session:

| Candidates from | Membership | Scope default |
|---|---|---|
| The tree, through `pick`'s own `--folder`, `--recursive`, `--tags-any` and `--tags-all` | **Live**: every reload runs `list` again. | `open` |
| `--ids` or `--from`: a **snapshot** | **Frozen**: the IDs given, and no others, for the whole session. | `all` |
| `--source <command>`: a **live source** | **Live**: every reload runs the command again. | `all` |

- **Task data is always live.** Every reload reads the tree again, whatever the candidates came from, so titles, readiness and completion are always current. Only *which* tasks are listed can be frozen.
- **A snapshot never grows.** A task created in the session, a task unblocked by an action, or a task created by another process is not added to it, even if upstream would have listed it. The emitted selection is therefore always a subset of the IDs given, which is what a pipeline downstream of `--from` can rely on. For a list that keeps up, use a live source.
- **Narrowing applies on top.** With a snapshot or a live source, `pick`'s scope, folder and tag filters still narrow the list. The scope defaults to `all`, so by default the picker shows exactly what upstream chose: `ftask list --readiness complete | ftask pick --from -` lists the complete tasks. `s` cycles the scope as usual.
- **Order** is always `pick`'s own (see [Lines](#lines)), not upstream's.

### Live source

`--source` runs a command for the first load and again on every reload, and takes the candidates from its output:

- **Run with `sh -c`**, in `pick`'s working directory and environment, with stdin from `/dev/null`. It is the user's own command, trusted as fzf's callbacks are.
- **Its output** must be one of the [accepted envelopes](#accepted-envelopes), as for `--from`: from `list` or `frontier`, perhaps through `jq`, or anything else that prints an ftask envelope with tasks. Its IDs are the candidates.
- **Its stderr** is captured, not passed to the terminal, where it would garble the picker.
- **Synchronous.** A reload waits for the command, and the picker doesn't respond meanwhile. A slow source makes a slow picker.
- **The first run's failures** end `pick` before fzf opens. An `ok: false` envelope passes through, with the source's own error kind and details, as a failed first load does (see [Errors](#errors)). Output that is not an accepted envelope is `invalid-input` (`field`: `/source`), with the first line of the command's stderr, if any, in the `reason`.
- **Later runs' failures** show in the status line, as `✗ source: …`, and the list stays as it was.

## Selecting at once

`--select-one` and `--exit-zero` let `pick` finish without showing the picker, as fzf's `--select-1` and `--exit-0` do:

- **Matched first, headlessly.** After the first load, `pick` matches `query` against the candidate lines with `fzf --filter`, with the same matching options the picker uses (so only title and tags are matched). The selection is recorded by Enter's callback (see [fzf contract](#fzf-contract)), and fzf's own `--select-1` skips it, so `pick` makes the decision itself.
- **One match** with `--select-one`: emit that candidate, exactly as if it were picked with Enter.
- **No match** with `--exit-zero`: emit an empty selection, as a quit does.
- **Otherwise** the picker opens as usual, with `query` already typed.
- **No terminal is needed** when the picker isn't shown. The [`unavailable`](#errors) check for `/dev/tty` is made only when it is about to be. fzf is still needed, for the match.
- An empty `query` matches every candidate, so `--select-one` picks the only task when there is one.

## Display

### Lines

One line per candidate task, in this order:

1. **Ready** tasks, in [frontier order](operations.md#frontier): priority highest first, unprioritized last, then ID lowest first.
2. **Blocked** tasks, in the same order.
3. **Complete** tasks (scope `all` only), `completed_at` newest first, then ID.

With an empty query, fzf shows this order. As the person types, fzf ranks by match, and ties keep this order (`--tiebreak=index`).

Each line has these columns:

```text
●  42  Book flights          #travel     /trips/japan  p2
◐  43  Book hotel            #travel     /trips/japan  →42
✓  12  Renew passport                    /trips
```

| Column | Content |
|---|---|
| State | `●` ready, `◐` blocked, `✓` complete. |
| ID | The task's ID. |
| Title | The title, whole. Titles hold no control characters, so a line is always one line. |
| Tags | Each tag as `#tag`, space-separated. |
| Folder | The folder path. |
| Detail | For a blocked task, `→` and its `blocking` IDs. Otherwise `p` and the priority, or nothing. |

- **Only the title and tags are matched.** The query never matches the ID, the folder or the state. Narrowing by folder is the [`f` action](#actions). (fzf's `--nth`.)
- **Color.** Blocked and complete lines are dimmed, and tags and folder are muted. With `NO_COLOR` set to a non-empty value, lines carry no color.
- **Duplicate IDs** show as one line per copy. An action on a duplicated ID fails with `conflict` (`rule`: `duplicate-id`), shown in the [status line](#status-line).

### Header, prompt and status line

- **Prompt** names the [mode](#modes) and the scope: `open> ` while typing, `[cmd] open> ` in command mode, and the value being asked for in a prompt (e.g. `priority 42> `).
- **Header** shows the scope folder, the filters in effect, and a one-line key hint for the current mode.
- <a id="status-line"></a>**Status line** (fzf's footer) shows the result of the last action, until the next one: `✓ completed 42, 43`, `✓ created 51`, or `✗ block 43 ← 7: conflict (acyclic): 7 → 43 → 7`. A failure shows its error kind and its `message`. If the last load reported warnings, it says `N warnings`.

### Preview

The preview pane shows the task under the cursor:

```text
#42 Book flights                       ready  p2
/trips/japan   #travel   created 2026-09-20   updated 2026-09-21
blocked by: —          blocks: 43
extra: status = waiting on quote
── notes ─────────────────────────────────────
Prefer ANA, aisle seat…
```

- **`blocks`** lists the open tasks whose `blocked_by` names this one. ftask stores no such list, so `pick` derives it from the whole-tree read it already holds, not from the scope.
- **`extra`** shows one `key = value` per key, each value as compact JSON, except that a string is shown bare.
- **Notes** are rendered with `glow` if it is on `PATH`, otherwise with `bat` (as Markdown, with color), otherwise as plain text. An empty or missing `.md` shows `(no notes)`.
- ctrl-/ toggles the pane in every mode.

## Modes

The picker always has one of four modes. Typing reaches the query only in insert mode and in the two modes that ask for input.

| Mode | Typing does | Enter | Esc |
|---|---|---|---|
| **insert** | Edits the query and filters the list. | Emits the selection and exits. | Enters command mode. |
| **command** | Runs [actions](#actions). Letters never reach the query. | Emits the selection and exits. | Quits: emits an empty selection and exits. |
| **prompt** | Edits a value (e.g. a priority) in the query line. Search is off, so the list stays put. | Applies the action, then back to command mode. | Cancels, back to command mode. |
| **choose** | Filters a second list, such as candidate blockers or folders. | Applies the action to the chosen items, then back to command mode with the task list. | Cancels, back to command mode with the task list. |

- **Start** in insert mode, with the query `--query` sets.
- **ctrl-space** enters command mode from insert mode, like Esc.
- **`i` or `/`** in command mode returns to insert mode, with the query kept.
- **Command mode is sticky.** Each action returns to it, so several actions in a row need no prefix.
- **ctrl-c** in any mode [cancels](#output): exits at once, with error kind `cancelled`. So do fzf's other abort keys (ctrl-g, ctrl-q).
- **Marks.** Tab (and, in command mode, space) marks or unmarks the line under the cursor, in insert, command and choose modes. Marks are cleared after every action, on every switch between the task list and a choose list, and by reloads that change the list.

## Actions

An action works on its **targets**: the marked tasks, or, with none marked, the task under the cursor. With no task under the cursor and none marked, it does nothing.

Command mode's keys:

| Key | Action | Runs | Targets |
|---|---|---|---|
| `c` | Complete or reopen: completes each open target and reopens each complete one. | [`complete`](operations.md#complete), [`reopen`](operations.md#reopen) | any |
| `e` | Edit notes: opens the targets' `notes_path`, all as arguments to one editor, with fzf suspended. `$VISUAL`, else `$EDITOR`, else `vi`. | none: ftask never sees notes edits | any |
| `n` | New task. Prompt `new> `, filled with the query. Creates an open task with that title in the scope folder (see below). | [`create`](operations.md#create) | none |
| `b` | Block. Choose list `blockers of 42> ` (see below). Adds the chosen tasks to each target's `blocked_by`. | [`block`](operations.md#block) | any |
| `u` | Unblock. Choose list `unblock 42> ` of the target's `blocked_by`, missing IDs included. Removes the chosen ones. | [`unblock`](operations.md#unblock) | one |
| `m` | Move. Choose list `move to> ` of every folder. Moves the targets there. | [`move`](operations.md#move) | any |
| `x` | Edit as JSON: opens the target's `title`, `priority`, `tags` and `extra` in `$VISUAL`, else `$EDITOR`, else `vi`, with fzf suspended, and applies what changed (see below). | `update` | one |
| `p` | Priority. Prompt `priority 42> `, filled with the current priority for one target. An integer sets it; `null` or empty clears it. | [`update`](operations.md#update) | any |
| `t` | Tags. Prompt `tags 42> `, filled with the current tags for one target (see below). | `update` | any |
| `s` | Scope: cycles `ready` → `open` → `all` → `ready` and reloads. | `list` | none |
| `f` | Folder. Choose list `folder> ` of every folder, `/` first. Sets the scope folder and reloads. | `list` | none |
| `r` | Reload. | `list` | none |
| `j` / `k` | Down / up. | — | — |
| `g` / `G` | First / last line. | — | — |
| `?` | Shows this table in the preview pane until the cursor moves. | — | — |
| `q` | Quit, as Esc. | — | — |
| `i`, `/` | Insert mode. | — | — |

- **One call per target.** An action on several targets runs its operation once per target, in the targets' list order. Each call stands alone: one that fails doesn't stop the rest, and the status line reports every outcome. A `busy` is reported like any other failure, never retried silently.
- **Reload after every action** that runs an operation, so the list, the readiness and the preview reflect it. A task that falls out of scope (e.g. completed while the scope is `open`) leaves the list. The status line still names it.
- **Blocker candidates.** `b`'s choose list holds every open task in the tree, regardless of the scope, except the targets and every task that depends on a target, directly or through others. Those would close a cycle. `block` still checks: a cycle created concurrently is refused with `conflict` (`acyclic`).
- **Tags syntax.** The value is a list of tags, separated by spaces or commas. If every item is bare (`travel urgent`), they replace all tags (`update`'s `tags.replace_all`). If every item is prefixed (`+urgent -later`), `+` adds and `-` removes. A mix is refused in the status line. Empty clears all tags.
- **New tasks** get the scope folder and the scope's `tags_all`, and no priority or blockers. With pick's own filters, that makes a new task a candidate unless `--tags-any` is given, which names no tag a new task could be given without guessing.
- **A new task is listed only if it is a candidate.** `pick` never adds a task to the list because it created it. After the reload, the cursor goes to the new task if it is listed. If it isn't, the status line says so (`✓ created 51 (not in this list)`), and it is reported in `actions` like any other. This happens with `--tags-any`, with a [snapshot](#candidates), which never grows, and with a [live source](#live-source) whose command doesn't return it.
- **Editing as JSON.** `x` writes the target's editable fields to a temp file in the session, as a pretty-printed JSON object, and opens it:
  ```json
  {
    "title": "Book flights",
    "priority": 2,
    "tags": ["travel"],
    "extra": {
      "status": "waiting on quote"
    }
  }
  ```
  On exit, `pick` compares the file with what it wrote, and runs one `update` with only what changed: a new `title` or `priority`; `tags.replace_all` if the tags differ as sets; `extra.merge` for keys added or changed and `extra.remove` for keys removed.
  - **Unchanged** (or unsaved): no operation, and the status line says so.
  - **Not valid**: not one JSON object, or keys other than those four. Nothing is applied, the status line says what is wrong, and the file is kept: the next `x` on the same task reopens it with the edits, instead of starting over. Any other action or a reload discards it.
  - **Changed elsewhere meanwhile**: only the fields edited are sent, so a concurrent change to another field survives. A concurrent change to the same field is overwritten: `update` has no compare-and-swap (see [Concurrent updates](#concurrent-updates)).
  - Notes are not in the file; they are `e`.
- **Values are checked by the operation.** A bad priority or a bad tag fails as the operation's `invalid-input`, shown in the status line, and the prompt stays open with the value kept, to be fixed or cancelled.

## Folder picker

`ftask pick --folders` lists the folders in scope, in [tree order](operations.md#tree-order), and emits the ones chosen in `result.folders`. It has insert and command modes, with movement, marking, `?`, Enter, Esc, `q` and ctrl-c as above, and no actions. The `f` and `m` choose lists use the same display.

## Output

`pick` writes one envelope to stdout after fzf exits, following the CLI spec's [Output](cli-spec.md#output) rules: compact, one line, delivered before exit.

- **Enter** emits the selection: the marked tasks or folders, or, with none marked, the one under the cursor. With an empty list, the selection is empty. `ok: true`, exit `0`.
- **Esc or `q` in command mode** quits: `ok: true` with an empty selection, exit `0`.
- **ctrl-c** (and fzf's other abort keys) cancels: `ok: false`, kind `cancelled`, exit `1`.

In every case, the envelope reports the actions taken.

**Output schema:**

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "pick-output",
  "oneOf": [
    {
      "type": "object",
      "required": ["tasks", "missing", "actions"],
      "properties": {
        "tasks": {
          "type": "array",
          "items": { "anyOf": [{ "$ref": "task-view" }, { "$ref": "task-projection" }] },
          "description": "The selected tasks, as read after the last action, in the order they were listed. Task views, or Task projections with fields. May be empty."
        },
        "missing": {
          "type": "array",
          "items": { "$ref": "task-file#/properties/id" },
          "description": "Selected IDs the final read did not find, e.g. deleted by another process meanwhile. Usually empty."
        },
        "actions": { "$ref": "#/$defs/actions" }
      },
      "additionalProperties": false
    },
    {
      "type": "object",
      "required": ["folders", "actions"],
      "properties": {
        "folders": { "type": "array", "items": { "$ref": "folder-path" }, "description": "The selected folders, in tree order. May be empty." },
        "actions": { "$ref": "#/$defs/actions", "description": "Always empty: the folder picker has no actions." }
      },
      "additionalProperties": false
    }
  ],
  "$defs": {
    "actions": {
      "type": "array",
      "description": "Every operation the session's actions ran, in the order run. Loads (list) and notes edits are not included.",
      "items": {
        "type": "object",
        "required": ["operation", "input", "output"],
        "properties": {
          "operation": { "type": "string", "enum": ["complete", "reopen", "create", "block", "unblock", "move", "update"] },
          "input": { "type": "object", "description": "The operation's input, as passed." },
          "output": { "$ref": "envelope", "description": "The operation's envelope, unchanged: success or failure." }
        },
        "additionalProperties": false
      }
    }
  }
}
```

- **The selection is read fresh.** The emitted tasks come from one final `list` read after fzf exits, so they reflect the session's changes and any others. They are not what the screen last showed.
- **Actions are reported whether they succeeded or failed.** `jq '.result.actions[] | select(.output.ok | not)'` finds the failures.
- **Warnings** are the final read's. An action's own warnings stay in its `output`.

## Errors

`pick` adds two CLI-only error kinds, like [`usage`](cli-spec.md#usage-errors). No operation raises them, and callers treat any kind they don't know as a generic failure.

| Kind | When | `details` |
|---|---|---|
| `unavailable` | The picker cannot run: no terminal, or no usable fzf. fzf is checked after `invalid-input` and before anything is read from the tree. The terminal is checked when the picker is about to be shown, after the first load (see [Selecting at once](#selecting-at-once)). | `reason`: `no-terminal` (`/dev/tty` doesn't open), `fzf-missing` (not on `PATH`), `fzf-too-old`, or `fzf-failed` (fzf exited with an error, e.g. a bad option in `FZF_DEFAULT_OPTS`). For `fzf-too-old`: `found` and `required`, the versions. For `fzf-failed`: `status`, fzf's exit status. |
| `cancelled` | The person cancelled with ctrl-c or another fzf abort key. | `actions`: as in the output, the operations already run. |

Otherwise:

- **The first load's errors** pass through: if the first `list` fails (e.g. `not-initialized`, or a `--folder` that doesn't exist), `pick` writes that envelope, with the operation's own kind and details, and never opens fzf.
- **Later loads' errors** (e.g. the scope folder deleted by another process) show in the status line. The list stays as it was.
- **Actions' errors** never end the picker. They show in the status line and are reported in `actions`.
- **A failure after the session.** If the final read fails after fzf exits (e.g. the root became unusable), `pick` writes that read's error envelope, with its own kind, and adds `actions` to its `details`, as `cancelled` does. A caller that finds `.error.details.actions` therefore knows what the session changed, whatever the kind.

**stderr** follows the CLI spec's [one-line rule](cli-spec.md#output), with one addition: fzf's own stderr (e.g. its message about a bad option) passes through to the terminal.

**Exit codes** are the CLI spec's: `0` for `ok: true`, `1` for `cancelled`, `unavailable` and other errors, `2` for usage errors. ctrl-c is a key in the picker, not a signal: it cancels with an envelope. A real signal (e.g. `kill -INT`) is still a crash, as the CLI spec's [Exit codes](cli-spec.md#exit-codes) say, and the actions already run stay in effect with no report.

## fzf contract

How `pick` drives fzf. This section is normative for behavior. The option spellings are illustrative.

- **One fzf process per session.** Modes, prompts and choose lists all switch inside it with `reload`, `rebind`/`unbind`, `enable-search`/`disable-search`, `change-prompt`, `change-query` and `transform-footer`. No nested fzf.
- **The terminal.** fzf draws on `/dev/tty`. `pick` gives fzf the first candidate lines on stdin, and sends fzf's stdout to `/dev/null`. The selection never comes from fzf's output, so options in `FZF_DEFAULT_OPTS` that change it (`--print-query`, `--expect`, `--print0`, `--read0`) cannot corrupt the result.
- **Callbacks.** Every key that does more than move or mark is bound to `transform(…)` calling back into the ftask binary (its own absolute path, from `os.Executable`) through an internal helper (see [Session](#session)). The helper does the work and prints the fzf actions to take next, e.g. `reload(…)+change-footer(✓ completed 42)+rebind(…)`. fzf runs `transform` synchronously, so callbacks never overlap.
- **The selection** is recorded by the helper: Enter's callback writes the selected IDs or folders to the session, then returns `accept`, and quit's writes an empty selection, then returns `accept`. The parent decides the outcome from the session and fzf's exit status: `0` with a recorded selection is Enter or quit, `130` is cancel, and anything else is `unavailable` (`fzf-failed`).
- **A shell of known syntax.** fzf runs callbacks with `--with-shell 'sh -c'`, whatever the user's `$SHELL`, and every argument is quoted for `sh`.
- **Line fields.** Each line is tab-delimited, starting with a hidden field (the ID, or the folder path) that callbacks receive as `{1}` / `{+1}`. Only that field identifies a line, never the displayed text.

### fzf options

- **`FZF_DEFAULT_OPTS`** (and `FZF_DEFAULT_OPTS_FILE`) are honored, as fzf honors them: colors, layout, borders, history.
- **`FTASK_PICK_OPTS`** is appended after `pick`'s own options, so it wins: e.g. `FTASK_PICK_OPTS='--height 60% --layout reverse'`.
- **Rebinding is at your own risk.** An option that rebinds a key `pick` uses (or `--disabled`, `--no-multi`, `--with-shell`) can break the modes. `pick` does not detect that.
- **`FZF_DEFAULT_COMMAND`** is never used: `pick` supplies every list.

## Session

The state a session keeps outside fzf, in a private temp directory (mode `0700`, under `$XDG_RUNTIME_DIR` if set, else the system temp directory). It is removed when `pick` exits, including on cancel. A crash may leave it behind. It is never under the root, so it is never part of the tree.

It holds:

- the **scope**: folder, recursive, readiness scope, filters, and `ids` or `source`;
- the **mode**, and for prompt and choose modes the action and its targets;
- the **action log**, appended one entry per operation run;
- the **selection**, once recorded;
- the **last whole-tree read**, for the preview's `blocks` and for `b`'s candidates.

**The helper** is a hidden command, `ftask __pick <verb> …`, that fzf's callbacks run. It reads the session directory from `FTASK_PICK_SESSION`, which `pick` sets in fzf's environment. It is internal: not listed in help, not part of the contract, and it may change in any release. Run with no valid session, it fails with `usage`.

## Departures from the CLI spec

Where `pick` differs from the [CLI spec](cli-spec.md)'s global rules, and why:

- **The intended user is a person** at a terminal, not an agent (CLI spec, introduction). `pick` is the one command that needs one, and fails fast without one.
- **Rendering for people.** The CLI renders nothing for people. `pick` renders lines and a preview, but only on the terminal. What it writes to stdout is still one JSON envelope.
- **Not the same everywhere.** A command's output doesn't depend on whether a terminal is attached. `pick` needs `/dev/tty`, but its stdout is the same whether stdout is a terminal, a pipe or a file.
- **An outside program.** fzf is a runtime dependency of `pick` alone. No other command needs it.
- **Two CLI-only error kinds,** `cancelled` and `unavailable`, besides `usage`.
- **Operations without passthrough.** The operations `pick` runs inside the session write nothing to stdout. Their envelopes are reported in `actions` instead.

## Testing

- **The helper is the unit.** Every action, mode switch and callback is a call to `ftask __pick` with a session directory, so it is tested without fzf: given a session and a key's arguments, check the fzf actions printed, the session after, and the operations run.
- **fzf end to end.** A smoke test drives a real fzf in a pseudo-terminal, with a scripted key sequence through fzf's `--listen` (or by writing keys to the pty). It covers each mode switch, one action of each kind, Enter, quit and cancel, and checks the envelope.
- **Minimum version.** The end-to-end test runs against fzf 0.63.0, the minimum, as well as the current release. Only actions that 0.63.0 has are used: marks are cleared with `deselect-all`, not `clear-multi`, which is 0.64.0's name for it.
- **Pipelines.** `--from` with each accepted envelope shape, and the rejections; stdin and stdout redirected while the terminal is the pty.

## Shipping

Done with the implementation, not before, since they describe a command that exists:

- **The skill** gets a hard rule: never run `ftask pick`. It needs the user's terminal; when the user wants to choose for themselves, suggest they run it. An envelope from `pick` that the user hands over is read like any other, `result.actions` included: it says what they changed.
- **The README** gets a section on picking tasks yourself, with the keymap's essentials and the pipeline examples.
- **cli-spec.md** moves `pick` from Planned commands to Commands.

## Future work

### Fields at creation

A mini-language in `n`'s prompt that sets more than the title, e.g. `Book hotel #travel !2` for tags `travel` and priority 2. Left out for now: titles are otherwise taken literally, and this would mean escaping `#` and `!` in them. Meanwhile, after `n` the cursor is on the new task when it is listed, so `t`, `p` and `x` are one key away.

### Concurrent updates

A change made elsewhere to a field being edited with `x` (or `p`, `t`) is overwritten. Letting [`update`](operations.md#update) take the `updated_at` the caller last saw, and refuse with `conflict` when the task has changed since, would close that: `pick` would pass the `updated_at` it loaded, and on a conflict keep the edits and show the newer task. This is an operation change, not a `pick` one.
