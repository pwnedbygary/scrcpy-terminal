package main

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"strings"
	"text/template"

	"scterm/peer"
)

// The completion scripts, as templates that completionScript fills in from
// scterm's own lists: the commands, the flags scterm -h lists, and the
// values validateConfig accepts.
var (
	//go:embed completion/scterm.bash
	bashCompletion string
	//go:embed completion/scterm.zsh
	zshCompletion string
	//go:embed completion/scterm.fish
	fishCompletion string
)

// completionShells are the shells scterm completion writes a script for.
var completionShells = []string{"bash", "zsh", "fish"}

// commandOptions are the options of these commands, which the scripts offer
// after them.
var commandOptions = map[string][]string{
	"peers":  {"--check"},
	"update": {"--check", "-y", "--yes"},
}

// flagValues says what completes the value of these flags. No other flag's
// value is completed: a serial would have to be asked of the adb server, and
// the rest are numbers, addresses and sizes.
var flagValues = map[string]string{
	"audio-codec":  "codec",
	"audio-source": "source",
	"peer":         "peer",
	"dump-frames":  "dir",
	"dump-audio":   "file",
}

// runCompletionCommand runs `scterm completion SHELL`, and `scterm
// __complete`, the scripts' hidden helper, and reports whether args named
// one of them.
func runCompletionCommand(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "__complete" {
		if len(args) < 2 || len(args) > 3 || args[1] != "peers" && args[1] != "forget" {
			usageError("usage: scterm __complete peers|forget [WORD]")
		}
		completePeers(os.Stdout, args[1] == "forget", args[2:])
		return true
	}
	if args[0] != "completion" {
		return false
	}
	const usage = "usage: scterm completion bash|zsh|fish"
	if len(args) > 1 && isHelpArg(args[1]) {
		fmt.Print(completionHelp)
		return true
	}
	if len(args) != 2 {
		usageError(usage)
	}
	script, ok := completionScript(args[1])
	if !ok {
		usageError(fmt.Sprintf("%s (%q is not one of these)", usage, args[1]))
	}
	fmt.Print(script)
	return true
}

const completionHelp = `usage: scterm completion bash|zsh|fish

Prints a script that completes scterm's commands and flags in that shell,
the values of --audio-codec and --audio-source, and the paired devices that
--peer and scterm forget take. The script never runs adb, so it does not
complete serials. To use it in bash or zsh, add its line to the shell's
startup file:

  bash, in ~/.bashrc:                  source <(scterm completion bash)
  zsh, in ~/.zshrc, after compinit:    source <(scterm completion zsh)

fish loads it from its completions directory; save it there again after
updating scterm:

  scterm completion fish > ~/.config/fish/completions/scterm.fish
`

// completionScript is the completion script for shell, if scterm has one.
func completionScript(shell string) (string, bool) {
	text, ok := map[string]string{"bash": bashCompletion, "zsh": zshCompletion, "fish": fishCompletion}[shell]
	if !ok {
		return "", false
	}
	data := struct {
		Commands, Flags, Codecs, Sources, Shells, Peers, Update []string
		// Kind lists the flags, without dashes, by what completes their
		// values: a kind of flagValues, "none" when nothing does, or "bool"
		// for a flag that takes no value.
		Kind map[string][]string
	}{Commands: commands, Codecs: audioCodecs, Sources: audioSources, Shells: completionShells,
		Peers: commandOptions["peers"], Update: commandOptions["update"], Kind: map[string][]string{}}
	for _, s := range helpSections {
		for _, f := range s.flags {
			names := strings.Fields(f.names)
			kind := "bool"
			if f.value != "" {
				kind = flagValues[names[len(names)-1]]
				if kind == "" {
					kind = "none"
				}
			}
			for _, name := range names {
				data.Flags = append(data.Flags, dashedFlag(name))
				data.Kind[kind] = append(data.Kind[kind], name)
			}
		}
	}
	var b strings.Builder
	if err := template.Must(template.New(shell).Funcs(completionFuncs).Parse(text)).Execute(&b, data); err != nil {
		panic(err)
	}
	return b.String(), true
}

// completionFuncs write lists of words, and of flags, as each script takes
// them. No word has a character that any of the shells reads specially.
var completionFuncs = template.FuncMap{
	"join": strings.Join,
	// either is a bash or zsh case pattern for the flags, each with one dash
	// and with two, as the flag package takes either.
	"either": func(names []string) string {
		var forms []string
		for _, n := range names {
			forms = append(forms, "-"+n, "--"+n)
		}
		return strings.Join(forms, "|")
	},
	// fish gives the flags, with or without their dashes, to fish's
	// complete: -l for a long name, and -o, for one dash, for a single
	// letter (-s would offer -nb, which the flag package does not take).
	"fish": func(names []string) string {
		var opts []string
		for _, n := range names {
			if n = strings.TrimLeft(n, "-"); len(n) == 1 {
				opts = append(opts, "-o "+n)
			} else {
				opts = append(opts, "-l "+n)
			}
		}
		return strings.Join(opts, " ")
	},
}

// dashedFlag is a flag's name as scterm -h writes it: one dash before a
// single letter, two before a longer name.
func dashedFlag(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

// completePeers prints the candidates for the pairing argument of --peer, or
// with forget of scterm forget, one to a line: for each pairing whose
// identity parses and that pasteArg names, what the script inserts for it. A
// name is the device's own text, so it comes from pasteArg's rule, which
// names a pairing by its name only when that is plain, selects the pairing
// alone, and no copy of it selects another, and otherwise by its identity.
// After scterm forget, when a pairing's identity does not parse, which no
// name selects, the word that forgets such pairings comes last. The pairings
// are read once, and searched as Store.Find searches them (peer.FindIn):
// pasteArg reads them again for each name and identity it tries, which for a
// few hundred pairings is a wait.
//
// With no args, a candidate is the value scterm is to get, for a shell that
// quotes what it inserts: fish, and zsh inside a quote.
//
// Otherwise args holds the word typed so far for the argument, as bash gives
// it (and zsh, outside quotes), and a candidate replaces that word as it is
// written: for each pairing whose name or identity starts with what the word
// says, in any case, pasteArg's argument, or, after a double quote the user
// opened, the name or identity in double quotes. A word with other quoting,
// or with an expansion, gets none.
func completePeers(w io.Writer, forget bool, args []string) {
	prefix, quote, ok := "", byte(0), true
	if len(args) > 0 {
		prefix, quote, ok = typedArg(args[0])
	}
	home, err := peerHome()
	if !ok || err != nil {
		return
	}
	recs, err := peer.OpenStore(home).All()
	if err != nil {
		return
	}
	// offer prints the candidate for arg, which gives scterm value.
	offer := func(arg, value string) {
		switch {
		case len(args) == 0:
			arg = value
		case !strings.HasPrefix(strings.ToLower(value), strings.ToLower(prefix)):
			return
		case quote == '"':
			arg = doubleQuoted(value)
		}
		fmt.Fprintln(w, arg)
	}
	find := func(query string) (peer.Record, error) { return peer.FindIn(recs, query) }
	for _, rec := range recs {
		fp, err := rec.ID()
		if err != nil {
			continue
		}
		arg, ok := pasteArgFor(recs, find, rec.Name, fp)
		if !ok {
			continue
		}
		value := arg // what arg gives scterm: the name pasteArg quoted, or an identity
		if strings.HasPrefix(arg, "'") {
			value = rec.Name
		}
		offer(arg, value)
	}
	if forget && len(damagedPairings(recs)) > 0 {
		offer(damagedArg, damagedArg)
	}
}

// typedArg reads word, an argument as typed so far on a bash command line,
// and returns what it says, and the quote it starts with and leaves open, if
// any. It reads an unquoted word, where a backslash takes the next character
// as it is, and a word that starts with a quote and has no other (nor, after
// a double quote, a backslash): ok is false for a word with other quoting, or
// with an expansion ($ or a backquote).
func typedArg(word string) (prefix string, quote byte, ok bool) {
	if rest, found := strings.CutPrefix(word, "'"); found {
		return rest, '\'', !strings.Contains(rest, "'")
	}
	if rest, found := strings.CutPrefix(word, `"`); found {
		return rest, '"', !strings.ContainsAny(rest, "\"\\$`")
	}
	var b strings.Builder
	for i := 0; i < len(word); i++ {
		switch c := word[i]; c {
		case '\'', '"', '$', '`':
			return "", 0, false
		case '\\':
			if i++; i < len(word) {
				b.WriteByte(word[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), 0, true
}

// doubleQuoted is s in double quotes, as bash reads it: a backslash before
// each \, ", $ and backquote, and each ! in single quotes between two
// double-quoted parts, since an interactive bash expands history even inside
// double quotes, and a backslash there would stay before the !.
func doubleQuoted(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '!':
			b.WriteString(`"'!'"`)
		case '\\', '"', '$', '`':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
