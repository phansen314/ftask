#!/usr/bin/env bash
# Adds the permission rules the ftask plugin can't ship itself (plugins
# cannot carry permissions) to ~/.claude/settings.json: every ftask command
# and jq run without prompting, except `ftask init` and the deletes, which
# ask: init changes this machine's setup, and a delete has no undo but git.
# The skill
# comes from the plugin; see the README.
#
#   scripts/install-claude.sh              # add the rules
#   scripts/install-claude.sh --uninstall  # remove them
#
# Safe to rerun. settings.json is backed up before it changes, and only the
# rules below are added or removed. Either way, a ~/.claude/skills/ftask link
# left by an earlier version of this script is removed, since the plugin now
# supplies the skill and the link would load it twice. Needs jq.
set -euo pipefail

claude_dir=${CLAUDE_CONFIG_DIR:-$HOME/.claude}
old_link=$claude_dir/skills/ftask
settings=$claude_dir/settings.json

allow='["Bash(ftask:*)", "Bash(jq:*)"]'
ask='["Bash(ftask init:*)", "Bash(ftask delete:*)", "Bash(ftask delete-folder:*)"]'

command -v jq >/dev/null || { echo "install-claude.sh needs jq" >&2; exit 1; }

case ${1:-} in
"") mode=install ;;
--uninstall) mode=uninstall ;;
*) echo "usage: $0 [--uninstall]" >&2; exit 2 ;;
esac

# edit_settings FILTER: rewrites settings.json through the jq FILTER, with
# $allow and $ask bound, after backing it up; does nothing if the result is
# unchanged.
edit_settings() {
	local cur new
	if [[ -e $settings ]]; then
		cur=$(<"$settings")
	else
		cur='{}'
	fi
	new=$(jq --argjson allow "$allow" --argjson ask "$ask" "$1" <<<"$cur")
	if [[ $(jq -S . <<<"$cur") == "$(jq -S . <<<"$new")" ]]; then
		echo "settings: $settings already up to date"
		return
	fi
	mkdir -p "$claude_dir"
	if [[ -e $settings ]]; then
		cp -p "$settings" "$settings.bak"
		echo "settings: backed up to $settings.bak"
	fi
	printf '%s\n' "$new" >"$settings.tmp"
	mv "$settings.tmp" "$settings"
	echo "settings: updated $settings"
}

if [[ -L $old_link && $(readlink "$old_link") == */claude/skills/ftask ]]; then
	rm "$old_link"
	echo "skill: removed the old link $old_link (the plugin supplies the skill)"
fi

if [[ $mode == install ]]; then
	# shellcheck disable=SC2016 # $allow and $ask are jq variables
	edit_settings '
		.permissions.allow = ((.permissions.allow // []) + ($allow - (.permissions.allow // [])))
		| .permissions.ask = ((.permissions.ask // []) + ($ask - (.permissions.ask // [])))'

	if ! command -v ftask >/dev/null; then
		echo
		echo "ftask is not on PATH. Install it with:"
		echo "  GOBIN=~/.local/bin go install github.com/phansen314/ftask/cmd/ftask@latest"
		echo "with GOBIN a directory on PATH."
	elif ! ftask info | jq -e .result.usable >/dev/null; then
		echo
		echo "ftask has no usable root yet. Set one up with, e.g.:"
		echo "  ftask init ~/ftasks"
	fi
	if command -v claude >/dev/null && ! claude plugin list --json 2>/dev/null | jq -e 'any(.[]; .id == "ftask@ftask" and .enabled)' >/dev/null; then
		echo
		echo "The ftask plugin is not installed. Install it with:"
		echo "  claude plugin marketplace add phansen314/ftask"
		echo "  claude plugin install ftask@ftask"
	fi
else
	# shellcheck disable=SC2016 # $allow and $ask are jq variables
	edit_settings '
		if .permissions then
			.permissions.allow = ((.permissions.allow // []) - $allow)
			| .permissions.ask = ((.permissions.ask // []) - $ask)
			| if .permissions.allow == [] then del(.permissions.allow) else . end
			| if .permissions.ask == [] then del(.permissions.ask) else . end
			| if .permissions == {} then del(.permissions) else . end
		else . end'
fi
