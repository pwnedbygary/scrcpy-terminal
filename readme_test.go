package main

import (
	"errors"
	"flag"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestReadmeListsEveryFlag: the flag reference in README.md names every flag
// newFlagSet defines, each in one row, under the heading scterm -h puts it
// under (--video, --audio and --control under the heading of their --no-
// forms), with one dash for a one-letter flag and two for the others, and
// names no flag that does not exist. Rows inside HTML comments do not count,
// since they do not render. --mirror-fps does nothing and stays out of both,
// as TestHelpListsEveryFlagOnce keeps it out of scterm -h. The Default column
// is not checked.
func TestReadmeListsEveryFlag(t *testing.T) {
	text, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := readmeFlags(string(text))
	if err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	fs := newFlagSet(&cfg, new(float64))
	fs.VisitAll(func(f *flag.Flag) {
		if _, ok := listed[f.Name]; !ok && f.Name != "mirror-fps" {
			t.Errorf("README.md does not list %s, which flags.go defines: add it to the first column of a table under ### Flags", dashed(f.Name))
		}
	})

	headings := map[string]string{}
	for _, s := range helpSections {
		for _, h := range s.flags {
			for _, name := range strings.Fields(h.names) {
				headings[name] = s.heading
			}
		}
	}
	for name, negated := range map[string]string{"video": "no-video", "audio": "no-audio", "control": "no-control"} {
		headings[name] = headings[negated]
	}

	for name, rows := range listed {
		switch {
		case fs.Lookup(name) == nil:
			t.Errorf("README.md lists %s under ### Flags, but flags.go defines no such flag", rows[0].word)
			continue
		case name == "mirror-fps":
			t.Errorf("README.md lists --mirror-fps, which does nothing and is left out of scterm -h")
			continue
		}
		if len(rows) > 1 {
			t.Errorf("README.md lists %s in %d rows; list each flag in one row", dashed(name), len(rows))
		}
		want, ok := headings[name]
		if !ok {
			t.Errorf("no scterm -h heading is known for %s: add it to helpSections or to this test", dashed(name))
		}
		for _, r := range rows {
			if r.word != dashed(name) {
				t.Errorf("README.md writes %s as %s", dashed(name), r.word)
			}
			if ok && r.heading != want {
				t.Errorf("README.md lists %s under %q; scterm -h puts it under %q", dashed(name), r.heading, want)
			}
		}
	}
}

// TestDoctorIsDocumented: scterm doctor is under Commands in scterm -h and in
// the README's table of commands, and the README's Troubleshooting sends the
// reader to it before its first problem. Both its help and that paragraph
// name the checks of --web's default address and of SCTERM_WEB_HOSTS.
func TestDoctorIsDocumented(t *testing.T) {
	cfg := defaultConfig()
	addr := net.JoinHostPort(cfg.webAddr, strconv.Itoa(cfg.webPort))
	for _, word := range []string{addr, webHostsEnv} {
		if !strings.Contains(doctorHelp, word) {
			t.Errorf("scterm doctor -h does not name %s", word)
		}
	}
	_, _, entries := readHelp(t)
	inHelp := false
	for _, e := range entries {
		if e.heading == "Commands" && strings.HasPrefix(strings.TrimSpace(e.lines[0]), "doctor ") {
			inHelp = true
		}
	}
	if !inHelp {
		t.Error("scterm -h does not list doctor under Commands")
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, table, _ := strings.Cut(string(readme), "\n### Commands\n")
	table, _, _ = strings.Cut(table, "\n#")
	if !strings.Contains(table, "\n| `scterm doctor` |") {
		t.Error("README.md does not list scterm doctor under ### Commands")
	}
	_, trouble, _ := strings.Cut(string(readme), "\n## Troubleshooting\n")
	first, _, _ := strings.Cut(trouble, "\n### ")
	if !strings.Contains(first, "`scterm doctor`") {
		t.Error("README.md's Troubleshooting does not start with scterm doctor, before its first ### problem")
	}
	for _, word := range []string{"`" + addr + "`", "`" + webHostsEnv + "`"} {
		if !strings.Contains(first, word) {
			t.Errorf("README.md's paragraph on scterm doctor does not name %s", word)
		}
	}
}

// TestDevelopmentNamesTheTestVariables: docs/DEVELOPMENT.md names each
// SCTERM_TEST_ variable that a test of this package sets for the child
// processes it starts, so that no one sets one by mistake.
func TestDevelopmentNamesTheTestVariables(t *testing.T) {
	doc, err := os.ReadFile("docs/DEVELOPMENT.md")
	if err != nil {
		t.Fatal(err)
	}
	tests, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, name := range tests {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range regexp.MustCompile(`SCTERM_TEST_[A-Z_]+`).FindAllString(string(src), -1) {
			if !seen[v] && !strings.Contains(string(doc), "`"+v+"`") {
				t.Errorf("docs/DEVELOPMENT.md does not name %s, which %s sets", v, name)
			}
			seen[v] = true
		}
	}
	if len(seen) == 0 {
		t.Error("no test sets an SCTERM_TEST_ variable: this test reads the wrong files")
	}
}

// dashed is a flag's name as the README and scterm -h write it.
func dashed(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

// readmeListing is one row of the flag reference that names a flag: the
// "####" heading above the row, and the word as written, with its dashes
// and without a value ("--control" for `--control=false`).
type readmeListing struct {
	heading, word string
}

// readmeFlags reads the flag reference: the tables after the "### Flags"
// heading, up to the next heading that is not a "####" one, leaving out HTML
// comments. It returns each flag named in the first column of a row, without
// its dashes or value, with every row that names it. Only words in code spans
// that start with a dash count, so `-s`, `--serial SERIAL` and
// `--control=false` name s, serial and control, and the wording around them is
// free.
func readmeFlags(readme string) (map[string][]readmeListing, error) {
	codeSpan := regexp.MustCompile("`([^`]*)`")
	readme = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(readme, "")
	flags := map[string][]readmeListing{}
	in, fenced, heading := false, false, ""
	for _, line := range strings.Split(readme, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			fenced = !fenced
			continue
		}
		switch {
		case fenced:
			continue
		case trimmed == "### Flags":
			in = true
			continue
		case !in:
			continue
		case strings.HasPrefix(trimmed, "#### "):
			heading = strings.TrimSpace(strings.TrimPrefix(trimmed, "#### "))
			continue
		case strings.HasPrefix(trimmed, "#"):
			return flags, nil // the next section: the reference ends here
		case !strings.HasPrefix(trimmed, "|"):
			continue
		}
		cells := strings.Split(trimmed, "|")
		if len(cells) < 3 {
			continue
		}
		inRow := map[string]bool{}
		for _, span := range codeSpan.FindAllStringSubmatch(cells[1], -1) {
			for _, word := range strings.FieldsFunc(span[1], func(r rune) bool { return r == ',' || r == ' ' }) {
				if !strings.HasPrefix(word, "-") {
					continue
				}
				word, _, _ = strings.Cut(word, "=")
				name := strings.TrimLeft(word, "-")
				if name == "" || inRow[name] {
					continue
				}
				inRow[name] = true
				flags[name] = append(flags[name], readmeListing{heading: heading, word: word})
			}
		}
	}
	if !in {
		return nil, errors.New(`README.md has no "### Flags" section`)
	}
	return flags, nil
}
