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
}
