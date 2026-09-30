# ftask

[![CI](https://github.com/phansen314/ftask/actions/workflows/ci.yml/badge.svg)](https://github.com/phansen314/ftask/actions/workflows/ci.yml)

Task management on one machine, with the filesystem as the database: tasks are JSON files in nested folders, with dependencies between them, and a Markdown notes file each. No daemon, no index, no server. Every command prints one line of JSON, so it is built to be driven by an agent such as Claude Code or OpenCode, with `jq` for anything a person reads.

Linux and macOS only.

**Pre-1.0:** until 1.0, the task file format, `ftask.json`, and the JSON output may change in place, with no `schema` bump or migration, so a new version can report an existing tree's files as `corrupt`. Each such change bumps the minor version (0.1 → 0.2), and its release notes say how to fix existing trees. See [Format versions](design-spec.md#format-versions).

## Install

Needs Go 1.25 or later.

```sh
GOBIN=~/.local/bin go install github.com/phansen314/ftask/cmd/ftask@latest   # any GOBIN on your PATH
ftask init ~/ftasks                                                          # this machine's task tree
```

## Use it from Claude Code and OpenCode

The [ftask skill](claude/skills/ftask/SKILL.md) teaches the agent the commands. Claude Code gets it from the `ftask` plugin (the repo is a Claude Code plugin marketplace):

```sh
claude plugin marketplace add phansen314/ftask
claude plugin install ftask@ftask
```

Then, from a clone of this repo, add the permission rules, which let ftask commands run without a prompt while `init`, which changes this machine's setup, and the deletes, which can't be undone but through git, still ask:

```sh
scripts/install.sh               # every agent whose CLI is on PATH
scripts/install.sh --opencode    # or name them: --claude, --opencode
scripts/install.sh --uninstall   # take it all out again
```

It needs `jq`, backs a settings file up (to `.bak`) before changing it, touches only ftask's rules, and is safe to rerun. For OpenCode it also links the skill into `~/.config/opencode/skills/ftask`, so OpenCode's skill comes from this clone: `git pull` updates it. Claude Code's comes from the plugin, and tracks `main`: `claude plugin update ftask@ftask` picks up changes, or turn on auto-update for the `ftask` marketplace in `/plugin`. To try an edited skill in Claude Code before pushing, run `claude --plugin-dir .` in a clone.

Then ask your agent things like "what should I work on next?" or "add a task to review the migration PR, blocked by 12".

### The rules, to add by hand

Claude Code, in `~/.claude/settings.json`:

```json
{
  "permissions": {
    "allow": ["Bash(ftask:*)", "Bash(jq:*)"],
    "ask": ["Bash(ftask init:*)", "Bash(ftask delete:*)", "Bash(ftask delete-folder:*)"]
  }
}
```

OpenCode, in `~/.config/opencode/opencode.json` (the script leaves an `opencode.jsonc`, or a file with comments, alone, and prints these for you to add). In OpenCode the last matching rule wins, so order matters: these go after any other rule that matches ftask, and the asks after `"ftask *"`:

```json
{
  "permission": {
    "bash": {
      "ftask *": "allow",
      "jq *": "allow",
      "ftask init*": "ask",
      "ftask delete *": "ask",
      "ftask delete-folder *": "ask"
    }
  }
}
```

For OpenCode, link the skill too: `ln -s "$PWD/claude/skills/ftask" ~/.config/opencode/skills/ftask` from the clone. Not in `~/.claude/skills`: OpenCode reads that as well, and Claude Code would load the skill a second time next to the plugin's.

OpenCode also asks before its file tools touch anything outside the project, which includes a task's notes file. To let it read and edit notes without asking, add your tree's root, for example `"external_directory": { "~/ftasks/*": "allow" }` under `"permission"`.

## A taste

```sh
ftask create-folder -p /work/api
ftask create 'Design schema' --folder /work/api --priority 2 --tags db
ftask create 'Write migrations' --folder /work/api --blocked-by 1
ftask frontier --limit 10 --fields id,title                       # the next ready tasks, in work order
ftask complete 1
```

## Undoing a delete

`ftask delete` and `ftask delete-folder` remove files for good; ftask keeps no trash. Keep the tree in git (`git init` in it, and commit now and then) and a delete can be undone, back to the last commit:

```sh
cd ~/ftasks
git log --diff-filter=D --oneline -- '*/42.json' '42.json'     # the commit that removed task 42, if committed
git restore -- proj/42.json proj/42.md                          # not yet committed: from the last commit
git restore --source=<commit>^ -- proj/42.json proj/42.md       # committed: from the commit before
```

Restore only the removed paths, never the whole tree: `ftask.json` holds the last issued ID, and rolling it back lets IDs be reused. The delete also took the task's ID out of other tasks' blockers; its output lists them as `dependents`, to re-`block` after restoring. See [Undo](operations.md#undo).

## Specs

- [design-spec.md](design-spec.md): the data model, invariants, concurrency, and crashes.
- [operations.md](operations.md): every operation's input, output, errors, and retry safety.
- [cli-spec.md](cli-spec.md): how commands and options map to operations.
- [implementation-spec.md](implementation-spec.md): how it is built and tested.

## Development

```sh
go test ./...        # unit and e2e tests
scripts/smoke.sh     # the built binary from a shell, in a throwaway home
```

## License

[MIT](LICENSE)
