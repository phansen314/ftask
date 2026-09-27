# ftask implementation spec

How ftask is built. The [design spec](design-spec.md), [operations](operations.md), and [CLI spec](cli-spec.md) say *what* ftask does; this document says *how*. Where they state a guarantee, this document gives the mechanism that provides it, and links back to the guarantee.

## Mechanism

How the design spec's [write lock](design-spec.md#write-lock) and [Guarantees](design-spec.md#guarantees) are provided.

- **The write lock is the root directory itself.** A write opens the root with `os.OpenRoot` (see [Filesystem access](#filesystem-access)), opens the directory itself through it (`root.Open(".")`) and `flock`s that descriptor. No lock file exists anywhere: nothing can be cleaned up, deleted, or recreated out from under a holder, and the root can never be deleted (see [Folders](design-spec.md#folders)). Because `flock` locks the directory, not a path, every process reaching the root — through any path spelling, symlink, or environment — contends on the same lock.
- **`flock`, never POSIX record locks.** A record lock (`fcntl`/`lockf`) is per process and is released when *any* descriptor to the file closes, so one incidental open-and-close inside a critical section silently drops it. Measured in an earlier prototype: a 16-way read-modify-write kept 8 updates under `lockf` and 16 under `flock`. `flock` is advisory and scoped to the open file description, which is the right scope — the hazard is two ftask processes, not two threads.
- **One resolution per write.** A write resolves the root path once, in `os.OpenRoot`, locks that directory through the same handle, and performs every file operation through that handle — never by re-resolving the path. Repointing a symlinked root mid-write therefore cannot split one write across two directories.
- **Close-on-exec.** The lock descriptor is close-on-exec so the lock cannot ride into a child program. Go opens every file close-on-exec by default.
- **Keep the lock's file alive.** The `*os.File` from `root.Open(".")` must stay referenced until the write ends. If it becomes unreachable, Go's finalizer may close the descriptor mid-write, which releases the lock. The write holds it explicitly and closes it (releasing the lock) only when done.
- **Non-blocking acquisition.** `LOCK_EX | LOCK_NB`; if the lock is held, fail immediately (see *Fail fast* in [Guarantees](design-spec.md#guarantees)).
- **Platform check.** `flock` on a directory descriptor is verified on Linux. On macOS it is confirmed from the XNU kernel source: `flock` accepts any descriptor on a filesystem, directories included; the advisory-lock layer rejects only FIFOs; and the local lock code never checks the file type. Its behavior matches Linux: the lock belongs to the open file description, is shared by `fork` and `dup` copies, and is advisory. An empirical check on macOS — contention, release on crash, close-on-exec — belongs in the implementation's test suite. Network filesystems are excluded (see [Assumptions](design-spec.md#assumptions)).
- **Atomic file writes.** Write a complete temp file in the same directory (a hidden entry), then publish it: `rename` to replace an existing file, `link` to create a new one so an existing file is never clobbered. A process crash between writing the temp file and removing it can leave the temp file behind; it is ignored by reads and reported by [`doctor`](design-spec.md#doctor).

## Toolchain

- **Go**, standard library first. The `version` operation's `go`, `commit`, `commit_time`, and `uncommitted_changes` come from the build information Go embeds (`runtime/debug.ReadBuildInfo`: the Go version and the `vcs.revision`, `vcs.time`, and `vcs.modified` settings); `version` itself is set at release build time. A **release build** must come from a git checkout, so this information is present; the release build fails otherwise. A **development build** without it reports `commit` `"unknown"` and `commit_time` `"1970-01-01T00:00:00Z"`, so the output still matches `version-output`.
- **No runtime dependencies** beyond the standard library. A JSON Schema library is a **test-only** dependency (see [Validation](#validation)).

## JSON reading

All JSON ftask reads — operation input, task files, `ftask.json` — goes through one reader, built on the standard library's token stream: `json.Decoder` with `UseNumber()`, read with `Token()`. The standard library does all the parsing: syntax, string escapes, number syntax. Decoding straight into structs or maps (`json.Unmarshal`) is never used, because it loses exactly what the specs need: repeated keys (the last one silently wins), the text of numbers (`2.0` and `2` become the same `float64`), and key order.

The reader builds an **ordered tree**: objects as ordered lists of members, arrays, strings, `true`/`false`/`null`, and numbers as `json.Number` — the literal's exact text. Its checks, and the standard library behavior each one covers:

| Check | Why the reader must do it |
|---|---|
| The raw bytes are valid UTF-8 (`utf8.Valid`), checked **before** decoding | The decoder silently replaces invalid bytes with U+FFFD; nothing after it can tell. |
| No byte-order mark | The decoder already rejects one; the reader reports it as its own reason. |
| Not empty | The decoder yields no tokens for empty input. |
| The top-level value is an object | The decoder accepts any value; operation input and every file must be an object (for a file, otherwise `not-json`). |
| No repeated key within an object | The token stream yields both keys; only the reader can see the repeat. |
| Nothing after the first value | The decoder is a stream and happily yields a second value. |
| No `\u` escape of an unpaired surrogate: a high one not followed by an escaped low one, or a low one alone | The decoder silently replaces it with U+FFFD, changing the string — and can make two distinct keys equal. Checked by a scan of the raw bytes after decoding. |
| At most 9,990 levels of nested objects and arrays | The token stream has no limit, but the encoder rejects output deeper than 10,000. The margin lets anything read be written back, even inside the output envelope, which adds at most 3 levels. |

How a failure is reported:

- **Operation input:** `invalid-input`, and nothing further is checked. `field` is `""`, except for a repeated key, where it is the pointer of that key (e.g. `/extra/status`).
- **A JSON option value** (e.g. `--extra`): reported at the option's field, with any inner pointer prefixed by it (a repeated key `status` in `--extra` is `/extra/status`), and other checks still run (see [Conversion](#conversion)). Its nesting counts from the input it sits in: `--extra` (at `/extra`) may nest 9,989 levels, `--extra-merge` (at `/extra/merge`) 9,988, so the input as a whole — and the task file written from it — stays within the limit.
- **A file:** a repeated key is a file-level rule, not a parse failure, so it must not pre-empt the version check ([File validity](design-spec.md#file-validity) step 2). The reader records it and keeps going; it is reported after the version check, as `corrupt` (`reason`: `invalid`). Every other failure above makes the file `corrupt` (`reason`: `not-json`).

**Adapters** then turn the tree into domain types (one per operation input, plus the task file and `ftask.json`), validating as they go (see [Validation](#validation)).

**Numbers in `extra` stay `json.Number`** from reading to writing and are never converted to `float64`. That is what lets [File format](design-spec.md#file-format) write them back character for character. A test round-trips `1.10`, `-0`, `1e400`, and a 20-digit integer through `update` unchanged.

## JSON writing

All JSON ftask writes — task files, `ftask.json`, and the CLI's envelope — uses the standard `json.Encoder`, configured so its output is exactly the design spec's [File format](design-spec.md#file-format) and the CLI's [Output](cli-spec.md#output):

- **`SetEscapeHTML(false)`**, always. The encoder then escapes exactly `"`, `\`, U+0000–U+001F, U+2028, and U+2029 — the File format's list.
- **`SetIndent("", "  ")`** for files. No indent for the CLI envelope, which is compact.
- **Key order** comes from struct field order, which follows each schema's key order.
- **`extra`** is an ordered-object type with its own `MarshalJSON`, emitting its members in stored order, so existing keys keep their position and new ones are appended. Objects nested inside `extra` use the same type.
- **Sets are sorted before writing:** `blocked_by` ascending, `tags` by name. The encoder writes arrays in the order given.
- **Trailing newline:** `Encode` writes one.

A test pins the exact bytes of a task file covering every rule above (key order, nested `extra`, empty `{}` and `[]`, sorted sets, raw `<>&/` and non-ASCII, escaped U+2028, exact `extra` numbers).

## Validation

### Where it happens

**At runtime, the adapters validate.** Converting the ordered tree to a domain type already requires checking each field's type; the adapter checks the field's remaining rules — the schema's constraints and the operation's Additional validation — at the same point, and records every problem it finds with the field's JSON Pointer. The binary contains no JSON Schema validator. One Additional validation check runs in its operation instead, because it needs the filesystem: `init`'s check that anything existing at `root` leads to a directory. It runs whenever `root` itself passed the adapter, and its problem joins the adapter's in the one `invalid-input`, which still reports every invalid input and comes first in `init`'s precedence. The only other value checks outside the adapters are the CLI's: the reading of JSON option values, and the input resolution a command lists — `init`'s `~user/` rejection and `--notes-file`'s UTF-8 check (see [Conversion](#conversion)).

**The published schemas are checked by tests only.** They stay normative, in the docs (see [Schemas in tests](#schemas-in-tests)).

### Order of checks

Each step adds to one list of problems; a failed step stops only what depends on it:

1. **Reading** (see [JSON reading](#json-reading)): UTF-8, no byte-order mark, not empty, no repeated key, one value. For operation input, a failure here stops everything below.
2. **Integer literals.** Outside `extra`, every number must be an integer literal (`2`, not `2.0` or `2e0`). This is exact because every number outside `extra`, in every input and file schema, is an integer; a test fails if a schema ever gains a non-integer number field outside `extra`. So the rule is checked with each integer field, and a number anywhere else fails its field's type: every such number is rejected either way, with the more specific reason.
3. **Field rules**, per adapter: type, required and unknown fields, patterns, lengths, ranges, uniqueness, and the `oneOf`/`anyOf` forms (e.g. `update`'s `tags`).
4. **Additional validation and file-level rules** no schema can express — title trimming and validation, real calendar timestamps, a task not in its own `blocked_by`, the filename ID matching `id`, `blockers` not containing `id` — run only on fields that passed step 3, so no field is reported twice.

Problems are sorted as [Error kinds](operations.md#error-kinds) requires (by `field`, then `reason`). A problem's `reason` is written for its field (e.g. "expected a JSON object"), never a generic validator message.

For files, the same steps implement [File validity](design-spec.md#file-validity)'s three steps: after step 1, `schema` must be present as an integer literal within ±(2^53 − 1) (else `corrupt`, `reason` `invalid`) and supported (else `unsupported-format`); then a recorded repeated key, and steps 2–4, make the file `corrupt` (`reason`: `invalid`). A repeated `schema` key makes the file `corrupt` (`invalid`) with no version check, since its version is ambiguous.

### Schemas in tests

- **Extraction.** A generator (`go generate`) extracts every JSON code block with an `$id` from the four specs into `schemas/`; an `$id` anywhere else is an error, so no schema is left out. A test regenerates and fails on any difference, so the docs and the tests' copies cannot drift.
- **Library.** The tests use a JSON Schema library that supports draft 2020-12 (including `$ref` by `$id` and `if`/`then`/`oneOf`), validates `json.Number` without converting it to `float64` (as a `float64`, `9007199254740993` rounds to a value that passes `priority`'s maximum), reports every error with its JSON Pointer, and loads schemas from local files under a fixed base URI. It is `github.com/santhosh-tekuri/jsonschema/v6`, with its regex engine replaced by Go's `regexp` after rewriting each `\uXXXX` escape to `\x{XXXX}`: the one ECMA-262 syntax the specs' patterns use that RE2 lacks. Syntax both accept with different meanings (`.` outside a class, `\s`, `\S`) is refused, so a pattern using it fails to compile. Only a test-helper package imports the library, so it never reaches the binary.
- **Agreement tests.** For every input schema and file schema, a corpus of cases — hand-picked edge cases plus valid inputs mutated one field at a time — goes through both the adapters and the schema library. Both must accept, or both must reject. On rejection, the adapter's problems must name exactly the fields the library names, with the library's errors about a child that its parent reports (a disallowed additional property, each array item equal to an earlier one, a missing required property) placed at that child, where the adapters report them. Where an `anyOf` or `oneOf` fails (`update`'s `tags` and `extra`, and its requirement of at least one field), the library reports every alternative's failures while the adapter reports only the form the input evidently meant: there the adapter's fields must be exactly one alternative's failures, and a property an alternative requires is placed at the object, since the alternative does not make it required overall. A file rejected for its `schema` at [File validity](design-spec.md#file-validity) step 1, or at step 2, is checked no further, so there the library need only also reject its `schema`. Integer literals and Additional validation are outside what the schemas express and are tested separately: adapters mark each problem from Additional validation (and each file-level rule beyond the file's schema), and the comparison leaves those out.
- **Output conformance.** Every envelope any test produces is validated against `envelope`, the operation's output or partial schema, `error`, `warning`, and — for usage errors — `usage-details`.

## Argument parsing

The standard `flag` package, `pflag`, and command frameworks do not implement the [Command line](cli-spec.md#command-line) rules (options and arguments in any order, `--no-` booleans, one spelling each, repeatable-only-when-marked, `--help` precedence, every problem reported). The parser is ftask's own, and small, because each command is described by a table rather than code.

### Command tables

Each command has one table row per argument and option: its name, any short form, the JSON Pointer of the field it sets (or none, e.g. `--notes-file`, `--help`), its value type (boolean, integer, nullable integer, string, folder path, comma list of IDs, comma list of tags, repeatable string, JSON value, file), whether it is required, and the options it is mutually exclusive with. `--help` text, the usage-error checks, and the input-building all come from the same table.

### Phases

1. **`--help`.** Scan for `--help` among the options — using the table, so a `--help` after `--` or consumed as an option's value is not one. If found, print help (the command's, if a known command is present) and exit `0`.
2. **Command.** The first token, unless it is `--help` or `--version`.
3. **Shape.** Walk the tokens against the command's table and collect every usage problem: unknown or misspelled options, missing values, values on booleans, repeats not marked repeatable, missing required arguments or options, extra arguments, two options setting one field, `--input` with field options. Problems are kept in the order [Usage errors](cli-spec.md#usage-errors) requires. If there are any, report `usage` (exit `2`) and stop.
4. **Build the input.** Place each value at its field, converting by type (below), and apply the command's input resolution (`init`'s `root`, `--notes-file`). With `--input`, read the file instead (then apply resolution).
5. **Validate** through the same adapters as `--input` (see [Validation](#validation)).

### Conversion

The CLI decides shape; values are judged by the adapters. A token that does not fit its field's type is passed on as it is, and the adapter rejects it at that field — exactly as it would the same value arriving through `--input`. The one exception is JSON option values:

- **Integer fields** (and each item of an ID list): a token shaped like a JSON integer (`-?(0|[1-9][0-9]*)`) becomes a number; the adapter checks its range. Anything else becomes a string, which the adapter rejects as the wrong type. `ftask show abc` builds `{"id": "abc"}` and fails at `/id`.
- **Nullable fields:** the token `null` becomes `null`.
- **JSON values** (`--extra`, `--extra-merge`, `--extra-replace-all`): the CLI checks the token with `json.Valid`. A valid token is parsed (through [JSON reading](#json-reading)) into that value. An invalid one is an `invalid-input` problem at the option's field ("not valid JSON"), raised by the CLI; the field is left out of the input, the adapters still run on the rest, and the CLI's problems are merged into the adapters' list before sorting, so every problem is still reported once.
- **Comma lists:** split on `,` exactly; `''` is `[]`. Items are not trimmed.
- **Strings, folder paths:** as given.
- **Input resolution** runs while the input is built. Its `invalid-input` problems (`~user/` in `init`'s `root`, non-UTF-8 `--notes-file` contents) are merged like a JSON option's. Its `io` errors (an unreadable `--notes-file` or `--input` file, an undeterminable working directory) and `environment` errors (an undeterminable home directory) stop the command before validation: the input cannot be built.

Apart from reading JSON option values and the input resolution a command lists (`init`'s `root`, `--notes-file`), the CLI holds no copy of any value rule, and a bad value is the same error whether it came from an argument or from `--input`. (If a malformed JSON option was the only field given to `update`, the adapters also report that no field was given; both problems are accurate.)

## Filesystem access

Every file operation under the root — reads and writes — goes through one `os.Root` (Go 1.25+, for `Root.Link` and `Root.Rename`), opened once per invocation with `os.OpenRoot` on the configured root path:

- **One resolution.** The root path is resolved once, when the `os.Root` is opened; every open, stat, `Mkdir`, `Link`, `Rename`, and `Remove` is relative to that handle. This is the [Mechanism](#mechanism)'s *one resolution per write*, in the standard library. (The `syscall` package has no `openat`, `renameat`, or `linkat` on macOS, so the handle is the portable way to get it.)
- **The lock** is taken on `root.Open(".")` — the same directory, through the same handle.
- **No symlinks followed.** `os.Root` follows symlinks that stay inside the root; ftask never does. `os.Root.OpenFile` ignores a caller's `O_NOFOLLOW`: it adds the flag itself and, on `ELOOP`, resolves the symlink when its target stays inside the root. So every file or folder ftask opens is first examined with `root.Lstat` — a symlink fails with `ELOOP` — and, once opened, its `Stat` must be `os.SameFile` as the `Lstat` result, or the open fails with `ELOOP` too. That second check catches an entry swapped for a symlink between the check and the open, portably (the `syscall` package has no `openat` on macOS). `os.Root` also follows in-root symlinks in a name's *earlier* components, which is why the path walk `Lstat`s each component in turn. Files are opened with `O_NONBLOCK`, so a FIFO where a file should be reads as empty instead of blocking the open. Tests confirm each case — a symlink to a file, a symlink to a folder, and a swap between check and open — on both platforms.
- **A non-directory root is `ENOTDIR`.** `os.OpenRoot` on a path leading to a regular file reports an error with no errno inside; `fsys` replaces it with `ENOTDIR`, so the [OS errors](#meaning-is-decided-where-the-call-is-made) row for `os.OpenRoot` applies.

## Tree walk

### The index

Operations that must find an ID, prove it absent, or return a collection walk the whole tree once, by **name only** — no file is read during the walk:

- **Order.** Depth-first, a folder before its subfolders, each folder's entries sorted by name — which yields folders in [tree order](operations.md#tree-order) (`/`, `/infra`, `/proj`, `/proj/travel`, `/proj-b`). Tasks within a folder are sorted by numeric ID (`9.json` before `10.json`).
- **Classification**, per [Walking the tree](design-spec.md#walking-the-tree): a hidden entry is skipped; a folder name that is a directory is descended into; `<id>.json` that is a regular file is a task; everything else, symlinks included, is skipped.
- **Result:** an index from each ID to every location that has it, in tree order; the list of folders; and the folders that could not be listed, with their errno.

`create` without `blocked_by`, `create-folder`, `version`, `info`, and `init` do not walk.

### Loading task files

Task files are loaded on first use and cached for the rest of the invocation. A load runs [File validity](design-spec.md#file-validity) through [JSON reading](#json-reading) and [Validation](#validation) and ends in either a usable task or an unusable one with its reason (`unreadable` with its errno, `corrupt` with its reason, or `unsupported-format`).

### Queries

Operations use the index through a few helpers:

- **Find exactly one** — the targets of `show`, `complete`, `reopen`, `block`, `unblock`, `update`.
- **Check existence** — `create`'s `blocked_by`, `block`'s blockers.
- **Filter by scope** — `frontier` and `list`: tasks and folders under `folder`, recursively or not.
- **Derive readiness** — for an open task only, evaluating every blocker even once one is known to block, per [Dependencies](design-spec.md#dependencies): no task file → blocking, `dangling-reference` (unless a folder the walk had to list was unreadable — then no warning, per [Warning kinds](operations.md#warning-kinds)); several → blocking, `duplicate-id`; unusable → blocking, `unusable-file`; complete → not blocking; open → blocking.

### Error precedence

[Precedence](operations.md#precedence) is the order the code runs in; each step returns on its first error:

1. validate input;
2. locate the config (`environment`), then check the root (config, `ftask.json`);
3. take the lock (writes only), then re-read `ftask.json`;
4. path walk of any input folder — a missing folder is held, not returned, so step 6 can report it in the same `not-found` as missing IDs; `corrupt` (`unexpected-file`) returns;
5. tree walk — a folder that cannot be listed is `io` here, for operations that must see the whole tree;
6. ID lookups: `not-found`, listing every missing ID together with any missing folder held from step 4. For `block`, `id`'s own task file(s) are loaded first — an unusable copy is reported then — because which blockers are new (and so looked up) depends on `id`'s `blocked_by`, across every copy;
7. load needed files (`corrupt`, `unsupported-format`: the first in tree order);
8. conflicts (`duplicate-id`, `acyclic`, `id-exhausted`).

`io` and `internal` return wherever they occur. `init` runs its own order.

### Warnings

One collector per invocation receives every warning, drops repeats by the spec's key (per file for `unusable-file`, per folder for `unreadable-folder`, per ID for `duplicate-id`, per (referring, missing) pair for `dangling-reference`, per `.md` for `notes-missing`), and sorts once at the end: by `kind`, then the first entry of `paths`, then `ids` ([Warnings](operations.md#warnings)).

### Concurrent writes during a read

Per the design spec's [Reads](design-spec.md#reads):

- A file that disappears between the walk and its load (`ENOENT`) is skipped silently.
- Before reporting an ID as duplicated, each of its locations is checked again with `root.Lstat`; locations that no longer exist are dropped. A task moved mid-read is then counted once, at the location seen last, and is not reported as a duplicate.

A needed file that has disappeared — in a read or a write — is treated as never found: `not-found` if it was the ID's only file (see [Precedence](operations.md#precedence)). So `show` never returns an empty `tasks`.

## Cycle check

Implements [`block`](operations.md#block)'s cycle check: adding "`id` is blocked by B" creates a cycle exactly when `id` is reachable from B by following `blocked_by`. The graph is keyed by ID ([Dependencies](design-spec.md#dependencies)): a duplicated ID is one node, with the union of every copy's `blocked_by` as its edges; complete tasks are followed like open ones; an ID with no task file has no edges.

### Phase 1: load the reachable subgraph

Starting from the new blockers (those not already in `id`'s `blocked_by`), follow `blocked_by` through the [index](#the-index), loading each task file once through the per-invocation cache and never expanding `id`:

- A node's edges are the union of all its copies' `blocked_by`, sorted ascending, without repeats.
- An ID with no task file is a node with no edges, skipped silently.
- An unusable copy is a needed-file error (`corrupt` or `unsupported-format`; the first in tree order, per [Precedence](operations.md#precedence)).

Phase 1 always completes before any cycle is looked for, so the set of files read — and therefore the errors — depends only on the graph.

### Phase 2: one breadth-first search per new blocker

For each new blocker B, in ascending order: breadth-first search from B over the loaded subgraph, with a first-in-first-out queue, visiting each node's neighbours in ascending ID order, never expanding `id`, and stopping when `id` is discovered. Parent pointers give the path `B → … → x → id`; the reported cycle is `[id, B, …, x]` — each task blocked by the next, the last blocked by `id`. If `id` is not reached, B creates no cycle.

Offending blockers are reported in ascending order in `ids`, with `cycles[i]` for `ids[i]`.

### Why the first path found is the one required

The spec requires the shortest cycle, ties broken by the lexicographically smallest ID sequence. Every candidate starts `[id, B]`, so this is the shortest, then lexicographically smallest, path from B to `id`. Breadth-first search with a FIFO queue and ascending neighbours finds exactly that, by induction on depth:

- **Claim:** nodes are dequeued in the lexicographic order of their shortest paths from B, and each node's parent lies on its lexicographically smallest shortest path.
- **Depth 0:** only B.
- **Step:** every depth-(k+1) path is a depth-k path with one node appended, so two such paths compare first by their depth-k prefixes, then by the appended ID. Depth-k nodes are dequeued in prefix order (by the claim), and each adds its undiscovered neighbours in ascending order; so depth-(k+1) nodes are discovered — and queued — in the lexicographic order of their paths, and a node's parent is set at its first discovery, which is through its smallest path.

So the first discovery of `id` yields the required path.

### Cycle-check tests

- **Brute force.** On thousands of small random graphs — with duplicated IDs, IDs with no task file, and complete tasks — compare against enumerating every simple path from B to `id` and picking the shortest, then lexicographically smallest. Results must match exactly.
- **Needed files.** A graph where B reaches both `id` and a corrupt file X, with X ordered after `id`: the result is `corrupt`, not `acyclic`.

## OS errors

`io` errors and the `unusable-file` (`unreadable`), `unreadable-folder`, and `notes-missing` warnings carry `code`: the symbolic OS error, never a number, and the same name on Linux and macOS ([Error kinds](operations.md#error-kinds)).

### Names

The standard library has no errno-to-name function (`syscall.Errno.Error()` is the message, e.g. "no space left on device"), and errno numbers differ by platform (`EAGAIN` is 11 on Linux, 35 on macOS). ftask keeps its own table, one per OS (`errno_linux.go`, `errno_darwin.go`), each written with `syscall` constants — `map[syscall.Errno]string{syscall.ENOSPC: "ENOSPC", …}` — so each carries its platform's numbers. They are separate files because some names exist on only one OS (`EL2NSYNC` on Linux, `EBADRPC` on macOS), and the completeness test needs them all.

- **Aliases** get one fixed name on every platform: `EAGAIN` (not `EWOULDBLOCK`), `ENOTSUP` (not `EOPNOTSUPP`), `EDEADLK` (not `EDEADLOCK`). The rule is about names, not numbers: where a platform gives the other name its own number (`EOPNOTSUPP` is 102 on macOS, `ENOTSUP` 45), that number also maps to the canonical name, and the other name never appears in output. The table can therefore map two numbers to one name; a test checks that each alias maps to its canonical name on the current platform.
- **Completeness test**, run on each platform: for every errno from 1 to 255 whose message is a real one (not "errno N"), the table must have a name. A missing name fails CI rather than shipping.
- **An errno not in the table** (e.g. from a newer kernel) is `internal` — never an invented name.

### Extraction

The errno is found with `errors.As(err, &errno)`, through `*os.PathError`, `*os.LinkError`, and `*os.SyscallError`. An error with no errno inside it is `internal`.

### Meaning is decided where the call is made

The same errno means different things in different places, so each call site classifies its own errors; only what no call site claims falls through to `io`:

| Where | errno | Becomes |
|---|---|---|
| `flock` on the root | `EAGAIN` | `busy` |
| `flock` | `EINTR` | retried |
| Path walk of an input folder | `ENOENT` | `not-found` |
| Path walk | `ELOOP` (from `O_NOFOLLOW` on a symlink), `ENOTDIR` | `corrupt` (`unexpected-file`) |
| Config or `ftask.json` | `ENOENT` | `not-initialized` (`missing`: `config` or `metadata`) |
| `ftask.json` | `ELOOP` (a symlink), `EISDIR` | `corrupt` (`unexpected-file`) |
| `os.OpenRoot` on the root | `ENOENT`, `ENOTDIR` | `not-initialized` (`missing`: `root`) |
| Loading a task file, in a read | `ENOENT` | skipped silently: it vanished ([Concurrent writes during a read](#concurrent-writes-during-a-read)) |
| Loading a task file, in a read | any other | `unusable-file` warning (`reason`: `unreadable`, with `code`) |
| Writing a task's `.md` (`create`) | any | `notes-missing` warning, with `code`; the operation succeeds |
| Loading a needed file, in a write | `ENOENT` | treated as never found ([Precedence](operations.md#precedence)) |
| Publishing a new file with `link` | `EEXIST` | `corrupt` (`unexpected-file`): a file where none can exist |
| Listing a folder, in `frontier` or `list` | any | `unreadable-folder` warning, with `code` |
| Anywhere else | any | `io`, with `path` and `code` |

[`info`](operations.md#info) classifies its own errors as state (see [info](#info)); no row applies to it.

### Paths

`path` is the root **as stored** in the config (with `~/` expanded), joined with the path relative to the root — as the design spec's [Root path](design-spec.md#root-path) requires for reported paths. Errors on the config itself use the config's path. `init`'s working-directory error uses `.` ([`init`](cli-spec.md#init)).

### OS-error tests

Beyond the completeness test, every row of the table above has a test. Faults that are easy to create for real are created: a folder with its permissions removed, a symlink in place of a folder, a file already at the publish path. The rest (`ENOSPC` on write, `EINTR` on `flock`) are injected through a test double for the filesystem calls.

## Exit and signals

Implements the CLI spec's [Output](cli-spec.md#output) and [Exit codes](cli-spec.md#exit-codes).

### One exit point

`main` calls `run()`, which returns the exit code; `main` then calls `os.Exit(code)`. Nothing else calls `os.Exit`, so deferred cleanup — removing temp files, closing the lock's file (which releases the lock) — always runs first. A process that dies by signal skips it; that is a crash, where a leftover temp file is expected and [`doctor`](design-spec.md#doctor) finds it.

### Writing the envelope

- The envelope and its newline are built in memory and written with one `os.Stdout.Write`, which retries short writes itself: the line is either written whole or the write returns an error.
- `os.Stdout.Close()` is then checked, since some errors surface only at close (e.g. stdout redirected to a network filesystem).
- Only when both succeed does ftask exit `0`, `1`, or `2`. If either fails, it writes a notice to stderr ("ftask: result not delivered: …"; human text, not part of the contract) and exits `3`.
- `--help` text is written the same way.

### SIGPIPE

By default the Go runtime kills a process with SIGPIPE when it writes to a closed pipe on stdout (exit 141). ftask calls `signal.Notify` for SIGPIPE at startup, which makes such a write return `EPIPE` instead; the write fails as above, and ftask exits `3` with the notice. Every "result not delivered" case therefore has the one exit code and the notice. (The CLI spec permits death by SIGPIPE; ftask does not use that latitude.)

### Interrupts

No handlers are installed for SIGINT, SIGTERM, or SIGHUP. Go's default terminates the process by the signal, which the shell sees as `128+n` — exactly the CLI spec's *interrupts are crashes*: no envelope, and the operation's Crash behavior and Retry safety apply.

### Crashes

An unrecovered panic or a fatal runtime error (out of memory, a detected data race) makes Go print a stack trace and exit with status **2** — the usage-error code, which tells the caller nothing ran. A crash mid-write means the opposite: the outcome is unknown.

- At startup, ftask calls `debug.SetTraceback("crash")`. A panic or fatal error then ends by SIGABRT, exit 134 (`128+6`), which the CLI spec classes as *outcome unknown*.
- **Panics are never recovered into `internal`.** Reporting `internal` (exit `1`) would claim the operation failed without effect, which a panic mid-write cannot promise. `internal` is reserved for bugs ftask detects as errors (an errno missing from the [table](#names), an impossible state); a write returns it like any other error, with `partial` where its operation defines one.

### Exit and signal tests

- Closed pipe: `ftask version | true`, with the reader gone before ftask writes → exit `3`, notice on stderr.
- Full disk: stdout redirected to `/dev/full` (Linux) → exit `3`.
- Signal: SIGTERM during a write held open by a test hook → exit 143, no envelope.
- Crash: a forced panic in a test build → exit 134, not 2.
- Delivery: every exit `0`, `1`, or `2` anywhere in the test suite comes with exactly one complete envelope line on stdout.

## Package layout

Module `github.com/phansen314/ftask`. Everything but `main` is under `internal/`: ftask's contract is the JSON its CLI emits ([Versioning](operations.md#versioning)), and a public Go API would be a second contract. The operations can be made a public package later if there is a reason to.

```text
cmd/ftask/                 main: SetTraceback, SIGPIPE, run(), os.Exit — nothing else
internal/cli/              argument parser, command tables, --help, conversion, envelope output, exit codes
internal/ops/              one file per operation: its input adapter and its steps, in precedence order
internal/model/            domain types: ID, Title, Tag, FolderPath, Priority, Timestamp, Extra (ordered), Task, TaskView
internal/store/            config, root states, the lock, tree walk and index, task-file cache, file validity, atomic writes
internal/graph/            readiness and the cycle check — pure functions, no I/O
internal/jsonio/           token-stream reader to ordered tree; encoder configuration; the ordered-object type
internal/fsys/             thin interface over os.Root and flock; the real implementation; a fault-injecting one
internal/errs/             error and warning kinds, the warning collector, the errno table
internal/buildinfo/        version, commit, commit time, uncommitted changes, Go version
internal/tools/schemagen/  go generate: extracts every $id schema from the four specs into schemas/
schemas/                   generated; used only by tests
e2e/                       end-to-end tests against the built binary
```

### Import direction

Each package imports only packages below it:

```text
cmd/ftask → cli → ops → store → fsys
                      ↘ graph      ↘ jsonio
                      ↘ model ←── (store, graph)
cmd/ftask → buildinfo (and ops → buildinfo, for version)
errs and jsonio may be imported by any package, and import none of ftask's own.
```

- **The CLI does not know the data model.** `cli` never imports `model` or `store`: it builds a `jsonio` tree from the command line, calls `ops.Run(name, tree, env)`, and writes the envelope it gets back. A composed command is defined in `ops` as a named composition (see [Operations and transactions](#operations-and-transactions)) and exposed by the CLI like any other name, so `cli` still composes nothing itself. The CLI spec's "no behavior beyond parsing arguments and composing operations" is thereby enforced by the compiler. Command tables hold field pointers and value types, which is CLI-spec knowledge, not model knowledge.
- **`graph` is pure.** Readiness and the [cycle check](#cycle-check) take already-loaded nodes; `store` does the loading. The brute-force comparison runs in memory.
- **`fsys` is the only package that touches the disk.** Its real implementation wraps `os.Root`, `Lstat`, `O_NOFOLLOW`, and `flock` ([Filesystem access](#filesystem-access)). Its fault implementation wraps the real one and, at a chosen call, returns an injected errno or ends the process — the one seam for the [OS error](#os-errors) tests and for crash injection.

### Operations and transactions

An operation is a function over a transaction: `func(tx *store.Tx, in Input) (Result, error)`. `store.Read(fn)` runs it without the lock; `store.Write(fn)` runs it holding the write lock, after re-reading `ftask.json`. A composed command is several operation functions inside one `store.Write` — the operations spec's "several operations under a single write lock" — so composition needs no change to this structure.

### Environment

`ops.Env` is passed in, never global: the config directory, the filesystem (real or fault-injecting), and a **clock**. The real clock returns UTC truncated to whole seconds ([Timestamps](design-spec.md#timestamps)); tests use a fixed clock, so written files can be compared byte for byte.

### Where each kind of test runs

- **In-process**, for speed: everything that does not depend on the process itself — operations, validation, the parser, the tree walk, the cycle check.
- **`e2e/`, against the built binary**, for what only a real process shows: exit codes, SIGPIPE, `/dev/full`, signals, crashes, and the lock between processes. Each test gets its own temp directory and `XDG_CONFIG_HOME`.

## Writing files

Every file ftask writes — task files, `.md` notes, `ftask.json`, the config — is published through a temp file, per [Mechanism](#mechanism)'s atomic file writes:

- **Name.** `.ftask-tmp-<random>`, in the directory of the file it will become: hidden (so reads ignore it), recognizably ftask's (so [`doctor`](design-spec.md#doctor) can find leftovers), and random (so two writes never collide).
- **Created exclusively** (`O_CREATE|O_EXCL`), written in full, then published: `link` to create a new file, so an existing one is never clobbered (`EEXIST` is `corrupt`, `unexpected-file`); `rename` to replace one. The temp file is then removed.
- **Mode** `0644` for files, `0755` for folders, before the umask.

## Config file

**Location.** ftask derives the config directory itself, per [Config file](design-spec.md#config-file), rather than with `os.UserConfigDir`, which fails on a relative `XDG_CONFIG_HOME` instead of ignoring it. The home directory is `$HOME`, used only when set to an absolute path; otherwise it is undeterminable, and anything that needs it fails with `environment` (`variable`: `HOME`).

The [config](design-spec.md#config-file) has exactly one key, so ftask reads it with its own parser for a strict subset of TOML rather than a TOML library:

- Blank lines, and comment lines starting with `#`, are ignored.
- Exactly one other line: `root = "<string>"`, with optional spaces around `=`. The string is a TOML basic string: `\"`, `\\`, `\t`, `\n`, `\uXXXX`, and `\UXXXXXXXX` escapes are decoded; any other escape, a missing closing quote, or trailing text other than a comment is `corrupt`.
- Anything else — no `root`, a repeated `root`, another key, a table, a literal (single-quoted) or multi-line string — is `corrupt` (`invalid`).
- The file must be UTF-8 without a byte-order mark.

Every failure above is `corrupt` with `reason` `invalid`; `not-json` does not apply, since the config is not JSON.

`init` writes the config as `root = "<path>"` plus a newline, escaping as TOML basic strings require.

**Move to a TOML library if the config ever gains more keys.** The subset parser is justified only because one key needs one line of syntax; a second key, a table, or any richer TOML makes a library (e.g. `github.com/BurntSushi/toml`) the right choice, with the same "anything unrecognized is `corrupt`" rule enforced on its result.

## `init`

`init` runs before any root exists, so it does not use the [`os.Root`](#filesystem-access) of other operations:

1. Validate and clean `root` ([`init`](cli-spec.md#init) path resolution first, in the CLI).
2. Check for an existing config (`config-exists` unless `replace_config`).
3. Create the root directory if needed — `os.Mkdir`, never `MkdirAll`: `init` never creates the root's parent.
4. Open the root with `os.OpenRoot`, then create `ftask.json` through it via a temp file and `link` (see [Writing files](#writing-files)), or read and check the existing one.
5. Create the config directory with `os.MkdirAll`, remove any `.ftask-tmp-*` left there by an earlier interrupted `init`, and write the config via a temp file in that directory: `link` when no config exists, `rename` under `replace_config`.

The config is written last, as [`init`](operations.md#init)'s crash behavior requires. A temp file left in the config directory is outside `doctor`'s reach, which is why step 5 removes stale ones.

## `info`

`info` inspects the config and `ftask.json` and reports every problem as state ([`info`](operations.md#info)). Each output field is derived as follows:

| Field | Value |
|---|---|
| `config.path` | the config file's path; `null` when it can't be located |
| `config.state` | `missing` on `ENOENT` or when the config can't be located; `unreadable` on any other OS error; `corrupt` if it fails the [subset parser](#config-file) or names a root in an illegal form; else `ok` |
| `config.root` | the root, cleaned and with `~/` expanded, when `config.state` is `ok`; else `null` |
| `tree` | `null` when `config.root` is `null` (including a `~/` root with no home directory to expand it into) |
| `tree.root_exists` | the root path leads, through symlinks, to a directory |
| `tree.metadata` | `missing` if there is no `ftask.json` (or no root); `unreadable` on an OS error reading it; else the [File validity](design-spec.md#file-validity) outcome: `corrupt`, `unsupported-format`, or `ok` |
| `tree.schema` | `ftask.json`'s `schema` whenever File validity step 1 passes; else `null` |
| `tree.last_id` | when `tree.metadata` is `ok`; else `null` |
| `initialized` | `false` exactly when the root is *not initialized* ([Root states](operations.md#root-states)): config `missing`, root missing, or `ftask.json` `missing`; `true` otherwise, including an unusable config (then `tree` is `null`) |
| `usable` | `config.state` and `tree.metadata` both `ok`, and `tree.root_exists` |
| `compatible` | `tree.schema` equals the supported `ftask.json` version; `null` when `tree.schema` is `null` |

## Comparing values in `update`

[`update`](operations.md#update) reports a field as changed only when its value differs *as a JSON value*: maps regardless of key order, `tags` as a set, numbers by numeric value. Numbers inside `extra` are `json.Number`, compared exactly with `math/big.Rat` (`SetString` parses decimal and exponent forms exactly), never through `float64`, which would call `1e400` equal to `2e400` or merge distinct 20-digit integers. Numbers outside `extra` are integers within ±(2^53−1) and compare as `int64`.

## Testing

### Test hooks

Tests that must pause a write or crash it at an exact point use hooks compiled only into a binary built with `-tags ftasktest`. The shipped binary contains no hooks, so no environment variable can make a real ftask pause or crash. `e2e/` builds and runs the tagged binary; a short smoke suite also runs the release build, to confirm the tag changes nothing else.

### Tests specified elsewhere

| Area | Tests | Section |
|---|---|---|
| JSON reading | the edge-case table: repeated keys, `2.0`, invalid UTF-8, byte-order mark, empty input, a second value | [JSON reading](#json-reading) |
| JSON writing | exact bytes of a task file; `extra` numbers round-tripped unchanged | [JSON writing](#json-writing) |
| Validation | adapter–schema agreement; output conformance of every envelope | [Schemas in tests](#schemas-in-tests) |
| Cycle check | brute-force comparison on random graphs; corrupt file ordered after `id` | [Cycle check](#cycle-check) |
| OS errors | errno-table completeness per platform; one test per call-site row | [OS errors](#os-errors) |
| Exit and signals | closed pipe, `/dev/full`, SIGTERM, forced panic → 134, one envelope per exit `0`/`1`/`2` | [Exit and signals](#exit-and-signals) |

### Lock

In `e2e/`, on Linux and macOS:

1. **Contention.** A write held open at a test hook; a second write gets `busy` (exit `1`), while reads still succeed.
2. **One lock however the root is reached.** The second writer comes in through a symlinked root path, and through a different config (another `XDG_CONFIG_HOME`) naming the same root: both get `busy`.
3. **Release on crash.** The holder is killed with SIGKILL; the next write succeeds, with nothing to clean up.
4. **Keep-alive.** The hook forces a garbage collection while the lock is held; the lock is still held ([Mechanism](#mechanism)).
5. **Stress.** 16 processes each create 50 tasks, retrying on `busy`. Afterwards: 800 tasks, all IDs distinct, `last_id` 800, no lost update.
6. **Racing `block`s.** `block A --blockers B` and `block B --blockers A` run concurrently, each retrying on `busy`, many rounds: in each, exactly one succeeds and the other ends `conflict` (`acyclic`).

The macOS run of this suite is the empirical check [Mechanism](#mechanism)'s platform check assigns to the test suite.

### Crash injection

Each write operation's **Crash behavior** lists what each step can leave behind; the fault-injecting [`fsys`](#import-direction) makes that testable. For every write, and every step *k* that changes the disk, the operation is run and the process killed with SIGKILL just before step *k*. Then:

- the tree is what Crash behavior says (e.g. `create` killed before step 2: `last_id` incremented, no task; before step 3: a task with no `.md`, whose notes read as empty);
- every invariant holds, as the design spec promises after a process crash;
- the next write succeeds — nothing is wedged;
- the only leftovers are temp files, the only thing `doctor` should find;
- rerunning the operation gives exactly the outcome its **Retry safety** claims.

**Errors midway.** The same steps with an injected errno instead of a kill: the envelope's `partial` matches the operation's partial schema and what took effect.

System crashes — steps lost or reordered by power loss — are not simulated: ftask makes no durability ordering promise, and the design spec leaves their consequences to `doctor`.

### Precedence tests

For each operation, a set of faults, each of which alone triggers one error kind: bad input, missing config, corrupt `ftask.json`, the lock held, a missing folder, a corrupt needed file, a duplicated ID, a cycle, and so on. Every pair of faults that applies to the operation is combined in one tree, and the error reported must be the one earlier in the operation's [precedence](operations.md#precedence) (or `init`'s own order). Two faults at the same step (e.g. two corrupt needed files) must report the first in [tree order](operations.md#tree-order).

### Generated and cross-cutting

- **Fuzzing** (Go's native fuzzer) of the JSON reader and the argument parser: any input yields a defined envelope and never a panic, since a panic is a crash (exit 134).
- **Determinism.** Every read runs twice over the same tree; the bytes must match, warnings and `problems` order included.
- **The CLI spec's examples.** Every `sh` block in the [CLI spec](cli-spec.md) runs against a fixture tree in CI and must exit as its context implies, producing JSON that `jq` accepts. Examples needing outside tools (`gh`, `$EDITOR`) are marked and skipped.
- **Race detector.** Every in-process test runs under `go test -race`.

### CI matrix

A CI matrix of Linux amd64 and macOS arm64, with the Go version pinned in `go.mod`. Every test runs on both, except `/dev/full`, which is Linux-only.
