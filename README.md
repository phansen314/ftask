# ftask

Task management on one machine, with the filesystem as the database: tasks are JSON files in nested folders, with dependencies between them, and a Markdown notes file each. No daemon, no index, no server. Every command prints one line of JSON, so it is built to be driven by Claude Code, with `jq` for anything a person reads.

Linux and macOS only.

## Install

Needs Go 1.25 or later.

```sh
go install github.com/phansen314/ftask/cmd/ftask@latest   # into $(go env GOPATH)/bin
ftask init ~/tasks                                        # this machine's task tree
```

## Use it from Claude Code

Needs `jq`.

```sh
git clone git@github.com:phansen314/ftask.git && cd ftask
scripts/install-claude.sh
```

This links the [ftask skill](claude/skills/ftask/SKILL.md) into `~/.claude/skills/ftask`, so a `git pull` updates it. It also adds three permission rules to `~/.claude/settings.json`, after backing the file up:

- allow `Bash(ftask:*)` and `Bash(jq:*)`, so ftask commands run without a prompt;
- ask `Bash(ftask init:*)`, since `init` changes this machine's setup.

Then ask Claude things like "what should I work on next?" or "add a task to review the migration PR, blocked by 12". `scripts/install-claude.sh --uninstall` removes the link and the rules.

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
