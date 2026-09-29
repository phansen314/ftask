#!/usr/bin/env bash
# Sets up Claude Code to use ftask, for the current user:
#
#   - links ~/.claude/skills/ftask to this repo's skill, so a git pull
#     updates it;
#   - adds permission rules to ~/.claude/settings.json: every ftask command
#     and jq run without prompting, except `ftask init`, which asks.
#
#   scripts/install-claude.sh              # install, or repair an install
#   scripts/install-claude.sh --uninstall  # remove the link and the rules
#
# Safe to rerun. settings.json is backed up before it changes, and only the
# rules below are added or removed. Needs jq.
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
skill_src=$repo/claude/skills/ftask
claude_dir=${CLAUDE_CONFIG_DIR:-$HOME/.claude}
skill_dst=$claude_dir/skills/ftask
settings=$claude_dir/settings.json

allow='["Bash(ftask:*)", "Bash(jq:*)"]'
ask='["Bash(ftask init:*)"]'

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

if [[ $mode == install ]]; then
	mkdir -p "$(dirname "$skill_dst")"
	if [[ -L $skill_dst || ! -e $skill_dst ]]; then
		ln -sfn "$skill_src" "$skill_dst"
		echo "skill: $skill_dst -> $skill_src"
	else
		echo "skill: $skill_dst exists and is not a link; move it aside and rerun" >&2
		exit 1
	fi

	# shellcheck disable=SC2016 # $allow and $ask are jq variables
	edit_settings '
		.permissions.allow = ((.permissions.allow // []) + ($allow - (.permissions.allow // [])))
		| .permissions.ask = ((.permissions.ask // []) + ($ask - (.permissions.ask // [])))'

	if ! command -v ftask >/dev/null; then
		echo
		echo "ftask is not on PATH. Install it with:"
		echo "  go install github.com/phansen314/ftask/cmd/ftask@latest"
		echo "and make sure \$(go env GOPATH)/bin is on PATH."
	elif ! ftask info | jq -e .result.usable >/dev/null; then
		echo
		echo "ftask has no usable root yet. Set one up with, e.g.:"
		echo "  ftask init ~/tasks"
	fi
else
	if [[ -L $skill_dst ]]; then
		rm "$skill_dst"
		echo "skill: removed $skill_dst"
	else
		echo "skill: no link at $skill_dst"
	fi

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
