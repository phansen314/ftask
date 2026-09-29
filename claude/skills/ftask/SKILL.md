---
name: ftask
description: Track and work through the user's tasks with the ftask CLI — a local, file-based task tree with dependencies. Use when the user mentions tasks, todos, their task list, "what should I work on next", blocking or unblocking work, marking something done, or ftask by name; and to offer (not silently create) a task for a follow-up found during other work.
---

# ftask

`ftask` keeps the user's tasks as files under one root directory per machine. Tasks live in folders, can block each other, and each has a notes file. Every command prints **one line of JSON** and nothing else.

## Before the first command

```sh
ftask info | jq -e .result.usable >/dev/null && echo ready
```

If not ready, `ftask info` says why. When no root is set up, tell the user and suggest `ftask init ~/tasks` (or a path they choose). **Never run `init` unasked** — it changes this machine's setup.

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

## Keep output small

Everything ftask prints lands in your context, and stays there for the rest of the session. A whole task is about 300 bytes, so a bare `ftask list` of a few hundred tasks is tens of KB. **Always pass `--limit` and `--fields` to `frontier` and `list`**, as below, and widen only when the question needs it:

- `--fields id,title,…` returns only those fields of each task (`id` always). Fields: `title`, `priority`, `folder`, `tags`, `readiness`, `blocking`, `blocked_by`, `extra`, `created_at`, `completed_at`, `notes_path`, `schema`.
- `--limit N` returns the first N in the command's order. The result always says `total` and `truncated`: when `truncated` is true there are `total` tasks and you got N. Say so rather than presenting N as everything, and fetch more only if the user needs them.
- `--tags-any a,b` / `--tags-all a,b` filter by tag, and `list --readiness …` by readiness. Anything else (`extra`, title words) is `jq`'s, still with `--fields` so less comes through.
- `--limit 0` gives just the count, in `total`.
- For one task's full detail, `show` it.

**Never trim a write's, `show`'s, or any small output's envelope with `jq`**: print it as is. When a big read needs `jq`, keep `ok`, `error` and `warnings` with `jq -c 'if .ok then .result |= <shape> else . end'`, where `<shape>` gives one value (`map(…)`, never `.[]`). A trimmed failure looks like success: the pipe's exit status is jq's, so the error kind and warnings just vanish.

## The daily loop

```sh
ftask frontier --limit 10 --fields id,title,priority,folder,tags   # what's ready, in work order
ftask show 42                                                      # one task, whole
ftask complete 42                                                  # done
```

`frontier` lists open, unblocked tasks in the order to work on them: highest priority first, unprioritized after, ties oldest (lowest ID) first. Scope it with `--folder /proj` and `--recursive=false`, or by tag with `--tags-any`.

`show` returns `result.tasks`, always an array: one task, or every copy if the ID is duplicated (with a `duplicate-id` warning — report it). Each has `readiness` (`ready`/`blocked`/`complete`), `blocking` (the blocker IDs still holding it up), and `notes_path`. **Notes are a plain Markdown file:** read it with the Read tool, and edit it with Edit/Write directly — there is no ftask command for notes after creation.

Overview of everything:

```sh
ftask list --limit 50 --fields id,title,readiness,folder           # open tasks, in tree order
ftask list --readiness blocked --limit 20 --fields id,title,blocking   # what's stuck, and on what
ftask list --folder /proj --tags-any urgent --limit 20 --fields id,title,readiness
ftask list --readiness complete --limit 0                          # how many are done: .result.total
ftask list --include-folders --limit 0                             # every folder: .result.folders
ftask list --fields id,title,extra --limit 200 | jq -c 'if .ok then .result |= (.tasks |= map(select(.extra.status == "waiting"))) else . end'
```

`list` returns open tasks (`ready` and `blocked`) unless `--readiness` says otherwise: `--readiness complete` for finished ones, `--readiness ready,blocked,complete` for all.

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
ftask delete 42                                # .result.dependents: the tasks it no longer blocks
ftask delete-folder -r /proj/old               # .result.ids: what went; .result.dependents: as for delete
```

- Moving never changes blockers: tasks are named by ID, not location. Notes move with the task.
- **Deleting is permanent.** ftask keeps no trash; the only undo is git, if the user keeps the tree in a repo, and only back to their last commit. So:
  - **Always confirm with the user before `delete` or `delete-folder`**, naming what goes (for a folder, list its tasks first, complete ones too: `ftask list --folder /x --readiness ready,blocked,complete --limit 50 --fields id,title,readiness`). Never delete to tidy up on your own initiative.
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
