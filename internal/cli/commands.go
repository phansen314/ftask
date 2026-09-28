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
