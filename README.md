# ftask

Task management on one machine, with the filesystem as the database: tasks are JSON files in nested folders, with dependencies between them, and a Markdown notes file each. No daemon, no index, no server. Every command prints one line of JSON, so it is built to be driven by Claude Code, with `jq` for anything a person reads.

Linux and macOS only.

## Install

Needs Go 1.25 or later.

```sh
GOBIN=~/.local/bin go install github.com/phansen314/ftask/cmd/ftask@latest   # any GOBIN on your PATH
ftask init ~/ftasks                                                          # this machine's task tree
```

## Use it from Claude Code

The repo is a Claude Code plugin marketplace; the `ftask` plugin carries the [ftask skill](claude/skills/ftask/SKILL.md).

```sh
claude plugin marketplace add phansen314/ftask
claude plugin install ftask@ftask
```

Plugins can't ship permission rules, so add these three to `~/.claude/settings.json` so ftask commands run without a prompt, while `init`, which changes this machine's setup, still asks:

```json
{
  "permissions": {
    "allow": ["Bash(ftask:*)", "Bash(jq:*)"],
    "ask": ["Bash(ftask init:*)"]
  }
}
```

Or, from a clone, `scripts/install-claude.sh` merges them in (backing the file up first, needs `jq`), and `--uninstall` takes them out again.

Then ask Claude things like "what should I work on next?" or "add a task to review the migration PR, blocked by 12".

The plugin tracks `main`: `claude plugin update ftask@ftask` picks up changes, or turn on auto-update for the `ftask` marketplace in `/plugin`. To try an edited skill before pushing, run `claude --plugin-dir .` in a clone.

## A taste

```sh
ftask create-folder -p /work/api
ftask create 'Design schema' --folder /work/api --priority 2 --tags db
ftask create 'Write migrations' --folder /work/api --blocked-by 1
ftask frontier | jq -r '.result.tasks[] | "\(.id)\t\(.title)"'   # ready tasks, in work order
ftask complete 1
```

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
