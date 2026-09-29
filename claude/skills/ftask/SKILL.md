---
name: ftask
description: Track and work through the user's tasks with the ftask CLI — a local, file-based task tree with dependencies. Use when the user mentions tasks, todos, their task list, "what should I work on next", blocking or unblocking work, marking something done, or ftask by name; and to offer (not silently create) a task for a follow-up found during other work.
---

# ftask

`ftask` keeps the user's tasks as files under one root directory per machine. Tasks live in folders, can block each other, and each has a notes file. Every command prints **one line of JSON** and nothing else; use `jq` to pick it apart.

## Before the first command

```sh
ftask info | jq -e .result.usable >/dev/null && echo ready
```

If not ready, `ftask info | jq .result` says why. When no root is set up, tell the user and suggest `ftask init ~/tasks` (or a path they choose). **Never run `init` unasked** — it changes this machine's setup.

## Reading output

Every command writes one envelope:

- success: `{"ok":true,"result":{…},"warnings":[…]}`
- failure: `{"ok":false,"error":{"kind":"…","message":"…","details":{…}},"warnings":[…]}`

Branch on `.error.kind`, not the exit code. **Always tell the user about any `warnings`** (unusable files, dangling blockers, duplicate IDs): they mean the tree needs attention.

| Exit | Meaning | What to do |
|---|---|---|
| 0 | Success | — |
| 1 | Operation error; see `.error.kind` | See below |
| 2 | Usage error (bad command line) | Fix the command; check `ftask <cmd> --help` |
| 3 or other | Outcome unknown (killed, stdout lost) | Reads: rerun. `create`: check with `list` before rerunning, or it may duplicate. Other writes are safe to rerun. |

Error kinds worth handling:

- `busy` — another write holds the lock (another session, maybe). Retry briefly:
  `for i in 1 2 3 4 5; do out=$(ftask complete 42); jq -e '.error.kind != "busy"' <<<"$out" >/dev/null && break; sleep 0.3; done; echo "$out"`
- `not-found` — `.error.details.ids` / `.folders` name what's missing. Folders are never created implicitly.
- `invalid-input` — `.error.details.problems[]` lists every bad field.
- `conflict` with `rule: "acyclic"` — the block would make a cycle; `.error.details.cycles` shows it.
- `conflict` with `rule: "not-empty"` — `delete-folder` without `-r` on a folder that holds tasks or folders. Don't add `-r` on your own: ask the user.
- `conflict` with `rule: "destination-exists"` — `move-folder` would land on a folder that already exists; folders are never merged.
- `conflict` with `rule: "duplicate-id"` or `"id-above-last-id"` — the tree is damaged; report it, don't work around it.
- `corrupt`, `unsupported-format`, `io`, `internal` — stop and report to the user; don't try to fix files by hand.

## The daily loop

```sh
ftask frontier | jq -r '.result.tasks[:10][] | "\(.id)\t\(.priority)\t\(.folder)\t\(.title)"'   # what's ready, in work order
ftask show 42 | jq '.result.tasks[0]'                                                         # one task
ftask complete 42 | jq -c '.result | {id, completed_at, changed}'                             # done
```

`frontier` lists open, unblocked tasks in the order to work on them: highest priority first, unprioritized after, ties oldest (lowest ID) first. Scope it with `--folder /proj` and `--recursive=false`.

`show` returns `result.tasks`, always an array: one task, or every copy if the ID is duplicated (with a `duplicate-id` warning — report it). Each has `readiness` (`ready`/`blocked`/`complete`), `blocking` (the blocker IDs still holding it up), and `notes_path`. **Notes are a plain Markdown file:** read it with the Read tool, and edit it with Edit/Write directly — there is no ftask command for notes after creation.

Overview of everything:

```sh
ftask list | jq -r '.result.tasks[] | "\(.id)\t\(.readiness)\t\(.folder)\t\(.title)"'
ftask list --include-complete --include-folders | jq '.result.folders'
ftask list --folder /proj | jq '[.result.tasks[] | select(.tags | index("urgent"))]'
```

There are no built-in filters or limits; filter with `jq`.

## Writing

```sh
ftask create-folder -p /proj/api                     # -p creates missing parents
ftask create 'Write migrations' --folder /proj/api --priority 2 --tags db,backend --blocked-by 41
ftask create 'Investigate flaky test' --notes-file - <<'EOF'
Seen in CI on 2026-09-28. Fails ~1 in 20 runs.
EOF
ftask update 42 --priority null --tags-add urgent --extra-merge '{"status":"waiting"}'
ftask block 42 --blockers 40,41                      # 40 and 41 must finish before 42
ftask unblock 42 --blockers 41
ftask reopen 42
```

Rules the commands enforce:

- **Folder paths are exact**, from the tree's root: `/`, `/proj/api`. Never `proj/api` or `/proj/`, and never derived from the working directory. Segments and tags: lowercase letters, digits, hyphens.
- **Titles**: one line, up to 200 characters. Quote them with single quotes. A title starting with `-` goes after `--`.
- **Priority**: an integer; **higher** sorts first in `frontier`. `null` means none (sorts after every priority, even negative ones).
- **Notes**: short text with `--notes`, anything longer through a quoted heredoc with `--notes-file -` (no escaping needed).
- `update` changes title, priority, tags (`--tags-add`, `--tags-remove`, `--tags-replace-all`), and `extra` (`--extra-merge`, `--extra-remove key`, `--extra-replace-all`). Blockers change only through `block`/`unblock`; completion only through `complete`/`reopen`.

## Moving and deleting

```sh
ftask move 42 --to /proj/api                   # a task into a folder; -p creates it
ftask move-folder /proj/api --to /archive      # into an existing folder: /archive/api
ftask move-folder /proj/api --to /proj/backend # otherwise to that path: a rename
ftask delete 42 | jq .result.dependents        # the tasks it no longer blocks
ftask delete-folder -r /proj/old | jq -c '.result | {ids, dependents}'
```

- Moving never changes blockers: tasks are named by ID, not location. Notes move with the task.
- **Deleting is permanent.** ftask keeps no trash; the only undo is git, if the user keeps the tree in a repo, and only back to their last commit. So:
  - **Always confirm with the user before `delete` or `delete-folder`**, naming what goes (for a folder, list its tasks first with `ftask list --folder /x`). Never delete to tidy up on your own initiative.
  - Prefer cancelling (below) when the user just means "won't do": it keeps the record.
  - A deleted task's ID is removed from its dependents' blockers, so they may become ready. Tell the user which (`dependents` in the output).

## Hard rules

- **Never** hand-edit, create, rename, or delete anything under the root except a task's notes `.md` — use `move`, `move-folder`, `delete`, and `delete-folder`. Task `.json` files and `ftask.json` belong to ftask; a hand edit can break invariants no command will repair.
- Don't pass `--input` unless building input from other JSON; flags are clearer.
- Don't create tasks the user didn't ask for. When a follow-up turns up during other work, offer it: "Want me to add a task for X?"

## Conventions

- **Cancelled**: tag it, then complete it — `ftask update 42 --tags-add cancelled && ftask complete 42`.
- **Waiting on someone/something**: `ftask update 42 --extra-merge '{"status":"waiting","on":"vendor quote"}'`; clear with `--extra-remove status --extra-remove on`.
- **Where a task came from**: put links (PR, issue, ticket) in the notes, not the title.
