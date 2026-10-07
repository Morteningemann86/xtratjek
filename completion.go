package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// completion.go — `tjek completion <shell>` and `tjek man`.
//
// Both are generated from one table, cliCommandSpecs, rather than written out
// per shell: three hand-maintained scripts plus a roff file is four places for
// a new subcommand to be forgotten. TestCompletionMatchesFlagSets keeps the
// table honest by comparing each command's flags against the flag.FlagSet the
// command itself builds, so a flag added to a command and not to the table
// fails the build rather than silently missing from completions.

// cliCommandSpec is one subcommand: its name, the one-line summary the
// completions show, and the long flags it accepts (without the leading dashes).
type cliCommandSpec struct {
	name    string
	summary string
	flags   []string
	// refArg marks the commands whose first positional is a task ref, so the
	// shells can complete it from the live task list.
	refArg bool
}

var cliCommandSpecs = []cliCommandSpec{
	{name: "add", summary: "add a new task", flags: []string{
		"chain", "comment", "depends", "due", "json", "like", "note", "p", "priority",
		"project", "quiet-id", "recur", "size", "stage", "start", "tag"}},
	{name: "list", summary: "list pending top-level tasks", flags: []string{
		"all", "blocked", "focus", "json", "limit", "project", "ready", "search",
		"search-re", "search-word", "sort", "stale", "tag", "unblocked-since", "wide"}},
	{name: "search", summary: "title/notes search", flags: []string{"json", "limit", "pending", "re", "sort", "word"}},
	{name: "top", summary: "show the top tasks by sequence score", flags: []string{"json", "n", "wide"}},
	{name: "show", summary: "show one task in full", flags: []string{"json"}, refArg: true},
	{name: "why", summary: "explain one task's sequence rank", flags: []string{"json"}, refArg: true},
	{name: "edit", summary: "change fields on one task", refArg: true, flags: []string{
		"add-dep", "add-tag", "append-note", "clear-due", "clear-note", "clear-project",
		"clear-start", "due", "note", "p", "priority", "project", "remove-dep", "remove-tag",
		"size", "stage", "start", "title"}},
	{name: "done", summary: "mark tasks done", flags: []string{"cascade", "comment", "m"}, refArg: true},
	{name: "reopen", summary: "move tasks back to pending", flags: []string{"comment", "m"}, refArg: true},
	{name: "delete", summary: "soft-delete a task", flags: []string{"f"}, refArg: true},
	{name: "undelete", summary: "restore a deleted task", flags: []string{"list"}},
	{name: "undo", summary: "restore the most recent deletion", flags: []string{"list"}},
	{name: "subtask", summary: "create a subtask", flags: []string{"each"}, refArg: true},
	{name: "comment", summary: "add, edit or delete a comment", flags: []string{"delete", "edit"}, refArg: true},
	{name: "start", summary: "start the time tracker", refArg: true},
	{name: "stop", summary: "stop the running time tracker"},
	{name: "log", summary: "log time against a task", refArg: true},
	{name: "tags", summary: "pending tags with counts", flags: []string{"json"}},
	{name: "projects", summary: "pending projects with counts", flags: []string{"json"}},
	{name: "stats", summary: "productivity summary", flags: []string{"format", "project", "search", "seq", "tag"}},
	{name: "export", summary: "write a JSON snapshot to stdout", flags: []string{"include-done"}},
	{name: "import", summary: "merge an export file into the store"},
	{name: "sync", summary: "sync with the configured server", flags: []string{
		"accept-stale", "adopt-local", "adopt-remote", "quiet", "recover", "save", "status", "token", "url"}},
	{name: "serve", summary: "run the sync server", flags: []string{"listen", "new-token", "tls-cert", "tls-key", "token"}},
	{name: "doctor", summary: "report this installation's health", flags: []string{"json"}},
	{name: "update", summary: "install the latest release", flags: []string{"check", "y"}},
	{name: "suggest", summary: "suggest dependency links between tasks", flags: []string{"list"}},
	{name: "remind", summary: "notify what is due today and overdue", flags: []string{"now"}},
	{name: "completion", summary: "print a shell completion script"},
	{name: "man", summary: "print the man page (roff)"},
	{name: "help", summary: "show the command reference"},
}

func cliCommandNames() []string {
	out := make([]string, 0, len(cliCommandSpecs))
	for _, c := range cliCommandSpecs {
		out = append(out, c.name)
	}
	sort.Strings(out)
	return out
}

func specFor(name string) (cliCommandSpec, bool) {
	for _, c := range cliCommandSpecs {
		if c.name == name {
			return c, true
		}
	}
	return cliCommandSpec{}, false
}

// cliCompletion implements `tjek completion <shell>`.
func cliCompletion(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: tjek completion bash|zsh|fish")
		return 2
	}
	var script string
	switch args[0] {
	case "bash":
		script = bashCompletion()
	case "zsh":
		script = zshCompletion()
	case "fish":
		script = fishCompletion()
	default:
		fmt.Fprintf(os.Stderr, "tjek completion: unknown shell %q (want bash, zsh or fish)\n", args[0])
		return 2
	}
	fmt.Print(script)
	return 0
}

// refCompletionNote explains the one dynamic completion: task refs come from
// `tjek list --json`, so the shells stay in sync with the store without this
// file knowing anything about it.
const refCompletionNote = "# Task refs are completed from the live store via `tjek list`."

func bashCompletion() string {
	var b strings.Builder
	b.WriteString("# bash completion for tjek. Install with:\n")
	b.WriteString("#   tjek completion bash > /etc/bash_completion.d/tjek\n")
	b.WriteString("#   (or source it from ~/.bashrc)\n")
	b.WriteString(refCompletionNote + "\n\n")
	b.WriteString("_tjek() {\n")
	b.WriteString("  local cur prev cmd\n")
	b.WriteString("  cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	b.WriteString("  cmd=\"${COMP_WORDS[1]}\"\n")
	b.WriteString("  if [ \"$COMP_CWORD\" -eq 1 ]; then\n")
	b.WriteString("    COMPREPLY=( $(compgen -W \"" + strings.Join(cliCommandNames(), " ") + "\" -- \"$cur\") )\n")
	b.WriteString("    return\n")
	b.WriteString("  fi\n")
	b.WriteString("  case \"$cmd\" in\n")
	for _, c := range cliCommandSpecs {
		if len(c.flags) == 0 && !c.refArg {
			continue
		}
		b.WriteString("    " + c.name + ")\n")
		if len(c.flags) > 0 {
			b.WriteString("      if [[ \"$cur\" == -* ]]; then\n")
			b.WriteString("        COMPREPLY=( $(compgen -W \"" + flagWords(c.flags) + "\" -- \"$cur\") )\n")
			b.WriteString("        return\n")
			b.WriteString("      fi\n")
		}
		if c.refArg {
			b.WriteString("      COMPREPLY=( $(compgen -W \"$(tjek list --json 2>/dev/null | sed -n 's/.*\"title\": *\"\\([^\"]*\\)\".*/\\1/p')\" -- \"$cur\") )\n")
		}
		b.WriteString("      ;;\n")
	}
	b.WriteString("    completion)\n")
	b.WriteString("      COMPREPLY=( $(compgen -W \"bash zsh fish\" -- \"$cur\") )\n")
	b.WriteString("      ;;\n")
	b.WriteString("  esac\n")
	b.WriteString("}\n")
	b.WriteString("complete -F _tjek tjek\n")
	return b.String()
}

func zshCompletion() string {
	var b strings.Builder
	b.WriteString("#compdef tjek\n")
	b.WriteString("# zsh completion for tjek. Install with:\n")
	b.WriteString("#   tjek completion zsh > \"${fpath[1]}/_tjek\"\n")
	b.WriteString(refCompletionNote + "\n\n")
	b.WriteString("_tjek() {\n")
	b.WriteString("  local -a commands\n")
	b.WriteString("  commands=(\n")
	for _, c := range cliCommandSpecs {
		b.WriteString("    '" + c.name + ":" + zshEscape(c.summary) + "'\n")
	}
	b.WriteString("  )\n")
	b.WriteString("  if (( CURRENT == 2 )); then\n")
	b.WriteString("    _describe -t commands 'tjek command' commands\n")
	b.WriteString("    return\n")
	b.WriteString("  fi\n")
	b.WriteString("  case \"${words[2]}\" in\n")
	for _, c := range cliCommandSpecs {
		if len(c.flags) == 0 && !c.refArg {
			continue
		}
		b.WriteString("    " + c.name + ")\n")
		if len(c.flags) > 0 {
			b.WriteString("      _arguments " + zshFlagArgs(c.flags))
			if c.refArg {
				b.WriteString(" '*:task:_tjek_refs'")
			}
			b.WriteString("\n")
		} else if c.refArg {
			b.WriteString("      _arguments '*:task:_tjek_refs'\n")
		}
		b.WriteString("      ;;\n")
	}
	b.WriteString("    completion)\n")
	b.WriteString("      _values shell bash zsh fish\n")
	b.WriteString("      ;;\n")
	b.WriteString("  esac\n")
	b.WriteString("}\n\n")
	b.WriteString("_tjek_refs() {\n")
	b.WriteString("  local -a refs\n")
	b.WriteString("  refs=(${(f)\"$(tjek list --json 2>/dev/null | sed -n 's/.*\"title\": *\"\\([^\"]*\\)\".*/\\1/p')\"})\n")
	b.WriteString("  _describe -t tasks task refs\n")
	b.WriteString("}\n")
	b.WriteString("_tjek \"$@\"\n")
	return b.String()
}

func fishCompletion() string {
	var b strings.Builder
	b.WriteString("# fish completion for tjek. Install with:\n")
	b.WriteString("#   tjek completion fish > ~/.config/fish/completions/tjek.fish\n")
	b.WriteString(refCompletionNote + "\n\n")
	b.WriteString("complete -c tjek -f\n")
	b.WriteString("function __tjek_refs\n")
	b.WriteString("  tjek list --json 2>/dev/null | string match -r '\"title\": *\"(.*)\"' -g\n")
	b.WriteString("end\n")
	for _, c := range cliCommandSpecs {
		b.WriteString("complete -c tjek -n '__fish_use_subcommand' -a " + c.name +
			" -d '" + fishEscape(c.summary) + "'\n")
	}
	for _, c := range cliCommandSpecs {
		for _, f := range c.flags {
			b.WriteString("complete -c tjek -n '__fish_seen_subcommand_from " + c.name + "' -l " + f + "\n")
		}
		if c.refArg {
			b.WriteString("complete -c tjek -n '__fish_seen_subcommand_from " + c.name +
				"' -a '(__tjek_refs)'\n")
		}
	}
	b.WriteString("complete -c tjek -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'\n")
	return b.String()
}

// flagWords renders the flags as bash/compgen words. Both single- and
// double-dash spellings are offered because Go's flag package accepts either.
func flagWords(flags []string) string {
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		out = append(out, "--"+f)
	}
	return strings.Join(out, " ")
}

func zshFlagArgs(flags []string) string {
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		out = append(out, "'--"+f+"'")
	}
	return strings.Join(out, " ")
}

func zshEscape(s string) string { return strings.ReplaceAll(s, "'", "''") }
func fishEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\\", "\\\\"), "'", "\\'")
}

// ── Man page ──────────────────────────────────────────────────────────────────

// cliMan prints a roff man page on stdout, for `tjek man >
// /usr/local/share/man/man1/tjek.1`. Generated rather than committed as a file
// so it cannot drift from the command table, and so the version line matches the
// binary that printed it.
func cliMan(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "usage: tjek man")
		return 2
	}
	fmt.Print(manPage())
	return 0
}

func manPage() string {
	var b strings.Builder
	// .TH sets the header; section 1 is user commands.
	fmt.Fprintf(&b, ".TH TJEK 1 \"\" \"tjek %s\" \"User Commands\"\n", appVersion)
	b.WriteString(".SH NAME\ntjek \\- keyboard-driven task manager for the terminal\n")
	b.WriteString(".SH SYNOPSIS\n.B tjek\n[\\fIcommand\\fR] [\\fIflags\\fR]\n")
	b.WriteString(".SH DESCRIPTION\n")
	b.WriteString("Run with no arguments, \\fBtjek\\fR launches its terminal UI: tasks, a\n")
	b.WriteString("calendar with time tracking, projects, tags, a kanban board and a stats\n")
	b.WriteString("dashboard, all keyboard-driven. Press \\fB?\\fR inside the app for the key\n")
	b.WriteString("reference, or \\fBctrl+k\\fR for the command palette.\n.PP\n")
	b.WriteString("Given a subcommand, it runs that command and exits, which is the scripting\n")
	b.WriteString("surface. Task references are a UUID prefix or a case-insensitive title\n")
	b.WriteString("substring; an ambiguous reference fails with exit code 2 and lists the\n")
	b.WriteString("candidates.\n")
	b.WriteString(".SH COMMANDS\n")
	for _, c := range cliCommandSpecs {
		fmt.Fprintf(&b, ".TP\n.B tjek %s\n%s.\n", c.name, manEscape(c.summary))
		if len(c.flags) > 0 {
			fmt.Fprintf(&b, "Flags: %s.\n", manEscape(flagWords(c.flags)))
		}
	}
	b.WriteString(".SH QUICK-ADD SYNTAX\n")
	b.WriteString("Titles accept inline tokens, in the app and in \\fBtjek add\\fR alike:\n")
	b.WriteString(".B #tag\n,\n.B @project\n,\n.B due:tomorrow\n,\n.B p:high\n,\n.B s:l\n,\n")
	b.WriteString(".B r:weekly\n and\n.B dep:^\n(the last-added task).\n")
	b.WriteString(".SH FILES\n")
	b.WriteString("Each file lives in the directory for its kind, which follows the platform:\n")
	b.WriteString("XDG on Linux (\\fI~/.config/tjek\\fR, \\fI~/.local/share/tjek\\fR,\n")
	b.WriteString("\\fI~/.local/state/tjek\\fR), %APPDATA% and %LOCALAPPDATA% on Windows,\n")
	b.WriteString("\\fI~/Library/Application Support\\fR on macOS. An existing \\fI~/.tjek\\fR\n")
	b.WriteString("holds all of them, and so does \\fBTJEK_HOME\\fR when set.\n")
	b.WriteString("\\fBtjek doctor\\fR prints the directories in use.\n")
	b.WriteString(".TP\n.I tasks.db\nThe task store (SQLite, WAL mode). Data directory.\n")
	b.WriteString(".TP\n.I settings.json\nPreferences: theme, language, sequencing weights, board columns. Config directory.\n")
	b.WriteString(".TP\n.I sync.json\nSync server URL and token, when configured. Config directory.\n")
	b.WriteString(".TP\n.I sync.log\nLocal edits that lost a sync conflict; \\fBtjek sync --recover\\fR reapplies them. State directory.\n")
	b.WriteString(".SH ENVIRONMENT\n")
	b.WriteString(".TP\n.B EDITOR\nEditor used for task notes. Falls back to notepad on Windows.\n")
	b.WriteString(".TP\n.B TJEK_SYNC_URL, TJEK_SYNC_TOKEN\nOverride the stored sync configuration.\n")
	b.WriteString(".TP\n.B TJEK_HOME\nKeep every file in this one directory.\n")
	b.WriteString(".SH EXIT STATUS\n")
	b.WriteString("0 on success, 1 on a runtime error, 2 on a usage error or an ambiguous\ntask reference.\n")
	b.WriteString(".SH SEE ALSO\n")
	b.WriteString("Full documentation at\n.UR https://github.com/Morteningemann86/xtratjek\n.UE\n")
	return b.String()
}

// manEscape protects the roff control characters that can appear in a summary
// or flag list: a leading dot would start a request, and a backslash escapes.
func manEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "-", "\\-")
	return s
}
