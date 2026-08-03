// Package completion generates shell completion scripts (bash, zsh, fish)
// for the gha-doctor CLI. Scripts are derived from the live flag registry,
// so they can never drift from the actual flags.
package completion

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/linnea-bakshi/gha-doctor/internal/lint"
)

// Shells lists the supported shells, in the order shown to users.
var Shells = []string{"bash", "zsh", "fish"}

// flagInfo is a normalized view of a CLI flag for script generation.
type flagInfo struct {
	Name       string // without dashes
	Usage      string
	IsBool     bool     // boolean flags take no argument
	Values     []string // fixed candidate values for the argument, if known
	IsDir      bool     // argument is a directory path
	IsFile     bool     // argument is a file path
	IsWorkflow bool     // argument is a workflow file in .github/workflows
}

// ruleIDs returns the fixable rule IDs (D001..Dxxx), sorted, excluding
// synthetic entries like "parse".
func ruleIDs() []string {
	ids := make([]string, 0, len(lint.RuleMeta))
	for id := range lint.RuleMeta {
		if strings.HasPrefix(id, "D") {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// collect builds flagInfo entries from a FlagSet.
func collect(fs *flag.FlagSet) []flagInfo {
	rules := ruleIDs()
	var out []flagInfo
	fs.VisitAll(func(f *flag.Flag) {
		fi := flagInfo{Name: f.Name, Usage: f.Usage}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			fi.IsBool = true
		}
		switch f.Name {
		case "explain", "disable":
			fi.Values = rules
		case "completion":
			fi.Values = Shells
		case "fail-on":
			fi.Values = []string{"any", "warning", "never"}
		case "run":
			fi.Values = []string{"latest"}
		case "dir":
			fi.IsDir = true
		case "workflow":
			// Complete from the current repo's own workflow files; the
			// flag also accepts display names, but file names are what a
			// shell can actually enumerate.
			fi.IsWorkflow = true
		case "badge", "score-history", "svg", "html", "prom":
			fi.IsFile = true
		}
		out = append(out, fi)
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Script writes the completion script for shell to w.
func Script(w io.Writer, shell string, fs *flag.FlagSet) error {
	flags := collect(fs)
	switch shell {
	case "bash":
		return bashScript(w, flags)
	case "zsh":
		return zshScript(w, flags)
	case "fish":
		return fishScript(w, flags)
	default:
		return fmt.Errorf("unsupported shell %q (supported: %s)", shell, strings.Join(Shells, ", "))
	}
}

func bashScript(w io.Writer, flags []flagInfo) error {
	var words []string
	for _, f := range flags {
		words = append(words, "--"+f.Name)
	}
	var cases strings.Builder
	for _, f := range flags {
		if f.IsBool {
			continue
		}
		pat := fmt.Sprintf("--%s|-%s", f.Name, f.Name)
		switch {
		case len(f.Values) > 0:
			fmt.Fprintf(&cases, "        %s)\n            COMPREPLY=( $(compgen -W %q -- \"$cur\") )\n            return\n            ;;\n", pat, strings.Join(f.Values, " "))
		case f.IsDir:
			fmt.Fprintf(&cases, "        %s)\n            COMPREPLY=( $(compgen -d -- \"$cur\") )\n            return\n            ;;\n", pat)
		case f.IsFile:
			fmt.Fprintf(&cases, "        %s)\n            COMPREPLY=( $(compgen -f -- \"$cur\") )\n            return\n            ;;\n", pat)
		case f.IsWorkflow:
			fmt.Fprintf(&cases, "        %s)\n            COMPREPLY=( $(compgen -W \"$(command ls .github/workflows 2>/dev/null)\" -- \"$cur\") )\n            return\n            ;;\n", pat)
		default:
			fmt.Fprintf(&cases, "        %s)\n            COMPREPLY=()\n            return\n            ;;\n", pat)
		}
	}
	_, err := fmt.Fprintf(w, `# bash completion for gha-doctor        -*- shell-script -*-
# Install: gha-doctor --completion bash > /etc/bash_completion.d/gha-doctor
# or:      eval "$(gha-doctor --completion bash)"
_gha_doctor() {
    local cur prev
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"
    case "$prev" in
%s    esac
    if [[ "$cur" == -* ]]; then
        COMPREPLY=( $(compgen -W %q -- "$cur") )
    fi
}
complete -F _gha_doctor gha-doctor
`, cases.String(), strings.Join(words, " "))
	return err
}

// zshDesc sanitizes a usage string for a zsh _arguments description:
// square brackets and colons are structural there.
func zshDesc(s string) string {
	r := strings.NewReplacer("[", "(", "]", ")", ":", "\\:", "'", "'\\''")
	return r.Replace(s)
}

func zshScript(w io.Writer, flags []flagInfo) error {
	var specs strings.Builder
	for _, f := range flags {
		desc := zshDesc(f.Usage)
		switch {
		case f.IsBool:
			fmt.Fprintf(&specs, "    '--%s[%s]' \\\n", f.Name, desc)
		case len(f.Values) > 0:
			fmt.Fprintf(&specs, "    '--%s=[%s]:%s:(%s)' \\\n", f.Name, desc, f.Name, strings.Join(f.Values, " "))
		case f.IsDir:
			fmt.Fprintf(&specs, "    '--%s=[%s]:%s:_files -/' \\\n", f.Name, desc, f.Name)
		case f.IsFile:
			fmt.Fprintf(&specs, "    '--%s=[%s]:%s:_files' \\\n", f.Name, desc, f.Name)
		case f.IsWorkflow:
			fmt.Fprintf(&specs, "    '--%s=[%s]:%s:{compadd -- $(command ls .github/workflows 2>/dev/null)}' \\\n", f.Name, desc, f.Name)
		default:
			fmt.Fprintf(&specs, "    '--%s=[%s]:%s:' \\\n", f.Name, desc, f.Name)
		}
	}
	_, err := fmt.Fprintf(w, `#compdef gha-doctor
# zsh completion for gha-doctor
# Install: gha-doctor --completion zsh > "${fpath[1]}/_gha-doctor"
_gha-doctor() {
  _arguments -S \
%s    && return 0
}
_gha-doctor "$@"
`, specs.String())
	return err
}

// fishEsc escapes a string for a single-quoted fish argument.
func fishEsc(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\\", "\\\\"), "'", "\\'")
}

func fishScript(w io.Writer, flags []flagInfo) error {
	var b strings.Builder
	b.WriteString("# fish completion for gha-doctor\n")
	b.WriteString("# Install: gha-doctor --completion fish > ~/.config/fish/completions/gha-doctor.fish\n")
	b.WriteString("complete -c gha-doctor -f\n")
	for _, f := range flags {
		// -o: Go-style single-dash long option; -l: GNU-style --flag.
		line := fmt.Sprintf("complete -c gha-doctor -o %s -l %s", f.Name, f.Name)
		switch {
		case f.IsBool:
			// no argument
		case len(f.Values) > 0:
			line += fmt.Sprintf(" -x -a '%s'", strings.Join(f.Values, " "))
		case f.IsDir:
			line += " -r -a '(__fish_complete_directories)'"
		case f.IsFile:
			line += " -r -F"
		case f.IsWorkflow:
			line += " -x -a '(command ls .github/workflows 2>/dev/null)'"
		default:
			line += " -r"
		}
		line += fmt.Sprintf(" -d '%s'\n", fishEsc(f.Usage))
		b.WriteString(line)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
