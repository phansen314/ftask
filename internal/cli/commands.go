package cli

// commands are ftask's commands, in help order (cli-spec.md, Commands).
var commands = []Command{
	{
		Name:    "version",
		Op:      "version",
		Summary: "Report the version and build of the ftask binary",
		Example: "  ftask version | jq -r .result.version",
	},
	{
		Name:    "info",
		Op:      "info",
		Summary: "Report the state of this machine's configured root",
		Example: "  ftask info | jq -e .result.usable >/dev/null && echo ready\n  ftask info | jq .result.config.root",
	},
	{
		Name:    "init",
		Op:      "init",
		Summary: "Create a new tree, or attach an existing one, as this machine's root",
		Args:    []Arg{{Name: "root", Field: "/root", Type: String}},
		Options: []Option{
			{Name: "replace-config", Field: "/replace_config", Type: Bool, Help: "replace an existing config"},
		},
		Example: `  ftask init ~/tasks
  ftask init tasks                    # relative to the working directory
  ftask init /mnt/usb/tasks --replace-config
  jq -n '{root: "~/tasks"}' | ftask init -i -`,
		Resolve: resolveRoot,
	},
	{
		Name:    "create-folder",
		Op:      "create-folder",
		Summary: "Create a folder, and optionally any missing parent folders",
		Args:    []Arg{{Name: "folder", Field: "/folder", Type: String}},
		Options: []Option{
			{Name: "parents", Short: "p", Field: "/parents", Type: Bool, Help: "create missing parent folders"},
		},
		Example: `  ftask create-folder /proj
  ftask create-folder -p /proj/travel/2026 | jq -r '.result.created[]'`,
	},
	{
		Name:    "create",
		Op:      "create",
		Summary: "Create a new, open task",
		Args:    []Arg{{Name: "title", Field: "/title", Type: String}},
		Options: []Option{
			{Name: "folder", Field: "/folder", Type: String, Help: "folder to create the task in, as an exact `path` (default /)"},
			{Name: "priority", Field: "/priority", Type: NullableInt, Help: "priority, an `int` or null (default null)"},
			{Name: "tags", Field: "/tags", Type: TagList, Help: "comma-separated `tags`"},
			{Name: "blocked-by", Field: "/blocked_by", Type: IDList, Help: "comma-separated `ids` of the tasks that block it"},
			{Name: "extra", Field: "/extra", Type: JSON, Help: "extra fields, a JSON `object`"},
			{Name: "notes", Field: "/notes", Type: String, Help: "initial notes `text`"},
			{Name: "notes-file", Field: "/notes", Type: TextFile, Help: "read the initial notes from `file` (- for stdin)"},
		},
		Exclusive: [][]string{{"notes", "notes-file"}},
		Example: `  ftask create 'Book flights' --folder /proj/travel --tags travel,urgent --priority 2
  ftask create 'Deploy' --blocked-by 41,42 | jq .result.id
  gh issue view 12 --json body -q .body | ftask create 'Fix login bug' --notes-file -
  ftask create 'Wait on quote' --extra '{"status":"waiting"}'`,
	},
	{
		Name:    "show",
		Op:      "show",
		Summary: "Return one task by ID, with its readiness and where its notes live",
		Args:    []Arg{{Name: "id", Field: "/id", Type: Int}},
		Example: `  ftask show 42 | jq '.result.tasks[0]'
  ftask show 42 | jq -r '.result.tasks[0].readiness'
  cat "$(ftask show 42 | jq -r '.result.tasks[0].notes_path')"
  for id in 41 42 43; do ftask show "$id"; done | jq -s '[.[].result.tasks[]?]'`,
	},
	{
		Name:    "complete",
		Op:      "complete",
		Summary: "Mark a task complete; completing a complete task changes nothing",
		Args:    []Arg{{Name: "id", Field: "/id", Type: Int}},
		Example: `  ftask complete 42 | jq .result.changed
  for id in 41 42; do ftask complete "$id"; done | jq -c '{id: .result.id, changed: .result.changed}'`,
	},
	{
		Name:    "reopen",
		Op:      "reopen",
		Summary: "Reopen a complete task; reopening an open task changes nothing",
		Args:    []Arg{{Name: "id", Field: "/id", Type: Int}},
		Example: "  ftask reopen 42 | jq -r .result.completed_at   # null",
	},
	{
		Name:    "update",
		Op:      "update",
		Summary: "Change a task's title, priority, tags, or extra",
		Args:    []Arg{{Name: "id", Field: "/id", Type: Int}},
		Options: []Option{
			{Name: "title", Field: "/title", Type: String, Help: "replace the title with `text`"},
			{Name: "priority", Field: "/priority", Type: NullableInt, Help: "set the priority, an `int`; null clears it"},
			{Name: "tags-add", Field: "/tags/add", Type: TagList, Help: "comma-separated `tags` to add"},
			{Name: "tags-remove", Field: "/tags/remove", Type: TagList, Help: "comma-separated `tags` to remove"},
			{Name: "tags-replace-all", Field: "/tags/replace_all", Type: TagList, Help: "the complete new `tags`; '' clears them"},
			{Name: "extra-merge", Field: "/extra/merge", Type: JSON, Help: "keys to set, a JSON `object`"},
			{Name: "extra-remove", Field: "/extra/remove", Type: Repeated, Help: "a `key` to delete; repeatable"},
			{Name: "extra-replace-all", Field: "/extra/replace_all", Type: JSON, Help: "the complete new extra, a JSON `object`; {} clears it"},
		},
		Example: `  ftask update 42 --priority 3 --tags-add urgent
  ftask update 42 --extra-merge '{"status":"waiting"}' | jq .result.changed
  ftask update 42 --priority null --tags-remove urgent --extra-remove status
  ftask update 42 --tags-replace-all ''`,
	},
	{
		Name:    "block",
		Op:      "block",
		Summary: "Add blockers to a task, all or nothing; a cycle is refused",
		Args:    []Arg{{Name: "id", Field: "/id", Type: Int}},
		Options: []Option{
			{Name: "blockers", Field: "/blockers", Type: IDList, Required: true, Help: "comma-separated `ids` of the tasks that block it"},
		},
		Example: `  ftask block 42 --blockers 41,43 | jq .result.added
  ftask block 42 --blockers 7 | jq -c 'select(.error.details.rule == "acyclic") | .error.details.cycles'`,
	},
	{
		Name:    "unblock",
		Op:      "unblock",
		Summary: "Remove blockers from a task; removing one that isn't there changes nothing",
		Args:    []Arg{{Name: "id", Field: "/id", Type: Int}},
		Options: []Option{
			{Name: "blockers", Field: "/blockers", Type: IDList, Required: true, Help: "comma-separated `ids` to remove from its blockers"},
		},
		Example: `  ftask unblock 42 --blockers 41 | jq .result.removed
  ftask unblock 42 --blockers 99   # clears a dangling reference to a task that no longer exists`,
	},
}
