package main

import (
	"errors"
	"flag"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestReadmeListsEveryFlag: the flag reference in README.md names every flag
// newFlagSet defines, each in one row, under the heading scterm -h puts it
// under (--video, --audio and --control under the heading of their --no-
// forms), with one dash for a one-letter flag and two for the others, and
// names no flag that does not exist. Rows inside HTML comments or code do not
// count, since they do not render as rows. --mirror-fps does nothing and
// stays out of both, as TestHelpListsEveryFlagOnce keeps it out of scterm -h.
// The Default column is not checked.
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

// codeSpan is a code span: the text between a backtick and the next one.
var codeSpan = regexp.MustCompile("`([^`]*)`")

// readmeFlags reads the flag reference: the tables after the "### Flags"
// heading, up to the next heading that is not a "####" one. It returns each
// flag named in the first column of a row, without its dashes or value, with
// every row that names it. Only words in code spans that start with a dash
// count, so `-s`, `--serial SERIAL` and `--control=false` name s, serial and
// control, and the wording around them is free.
//
// What GitHub shows as code, or hides as an HTML comment, is no row. The rules
// follow GFM's tables and CommonMark's code, HTML blocks and raw HTML (specs
// 0.29-gfm and 0.31.2, cited from memory). A line indented by four columns or
// more, a tab going on to the next multiple of four, neither starts nor
// continues a row: outside lists and block quotes, which the reference does
// not use, it is code, or more of a paragraph. Code fenced with three or more
// backticks or tildes runs up to a fence of the same character at least as
// long; backticks with a backtick after them on the line are no fence. A line
// that starts with "<!--", after three spaces at most, starts an HTML block
// that hides it and each line after it, through the one that holds "-->".
// Every other line that starts with "|" is a row. GFM splits a row into cells
// at its pipes before it reads them, so a comment ends with its cell: in the
// first cell, a comment that opens and closes in the cell is taken out, and
// one that does not close there is plain text, as is "<!--" in a code span.
// In a line that is no row, such as a heading, a comment that opens and closes
// on the line is taken out; "<!-->" and "<!--->" are whole comments.
//
// readmeFlags differs from GitHub in other ways, which the reference does not
// use; among them: It splits a row at escaped pipes too, which GitHub keeps in
// the cell. It does not look for the header and delimiter rows that GitHub
// needs above a table's rows, so a row cut off from its table counts, where
// GitHub shows text. It knows no HTML block but a comment, so rows under a
// <div>, <details> or <pre> line count, where GitHub shows HTML; no setext
// heading, so one does not end the reference; and no list item, so a fence or
// a "<!--" on a list item's line opens nothing and the rows after it count
// (and the fence's closing line opens code that hides what follows). It
// misses rows without a leading pipe or with a single pipe, code spans of two
// backticks, and tables in a block quote or in a list item whose content
// starts four or more columns in (a nested one, or one after a marker such as
// "10."). A closing fence with white space after it other than spaces, tabs
// and a line ending's carriage return (a no-break space or a form feed, say)
// ends the code, where GitHub keeps it open.
// It reads a heading as written: a closing run of "#", or a code span's
// backticks, stay in a "####" heading's name; "### Flags ###" is not the
// section; and any other line that starts with "#" ends the reference, even
// one GitHub shows as a paragraph ("#5", "#######") or as a "####" heading
// (a bare "####", or "####" and a tab).
//
// A row it misses goes unchecked: its flag fails the test as unlisted unless
// another row names it, and a stale or second row there goes unseen. A row it
// counts that GitHub does not show can hide a flag the README does not show,
// or fail the test: as a second row, under another heading, written with the
// wrong number of dashes, for --mirror-fps, which scterm -h leaves out, or for
// a flag flags.go does not define.
func readmeFlags(readme string) (map[string][]readmeListing, error) {
	flags := map[string][]readmeListing{}
	in, heading := false, ""
	fence := ""      // the fence of the code the line is in, if any
	comment := false // the line is in an HTML block that a "<!--" line started
	for _, line := range strings.Split(readme, "\n") {
		if comment {
			comment = !strings.Contains(line, "-->")
			continue
		}
		if fence != "" {
			if closesFence(line, fence) {
				fence = ""
			}
			continue
		}
		if indentation(line) >= 4 {
			continue // code, or more of a paragraph
		}
		if fence = opensFence(line); fence != "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<!--") {
			comment = !strings.Contains(trimmed, "-->")
			continue
		}
		if !strings.HasPrefix(trimmed, "|") {
			trimmed = strings.TrimSpace(uncommented(trimmed))
		}
		switch {
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
		for _, span := range codeSpan.FindAllStringSubmatch(uncommented(cells[1]), -1) {
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

// indentation is how many columns line is indented by, a tab going on to the
// next multiple of four.
func indentation(line string) int {
	n := 0
	for _, c := range line {
		switch c {
		case ' ':
			n++
		case '\t':
			n += 4 - n%4
		default:
			return n
		}
	}
	return n
}

// opensFence is the fence that line opens code with (three or more backticks,
// or tildes, indented by three columns at most), or "".
func opensFence(line string) string {
	if indentation(line) > 3 {
		return ""
	}
	s := strings.TrimLeft(line, " \t")
	for _, c := range "`~" {
		fence := s[:len(s)-len(strings.TrimLeft(s, string(c)))]
		// The info string after backticks has none of its own.
		if len(fence) >= 3 && (c == '~' || !strings.Contains(s[len(fence):], "`")) {
			return fence
		}
	}
	return ""
}

// closesFence reports whether line ends the code that fence opened: a fence of
// the same character, at least as long, with only white space after it.
func closesFence(line, fence string) bool {
	if indentation(line) > 3 {
		return false
	}
	s := strings.TrimLeft(line, " \t")
	rest := strings.TrimLeft(s, fence[:1])
	return len(s)-len(rest) >= len(fence) && strings.TrimSpace(rest) == ""
}

// uncommented is line, or a row's cell, without the HTML comments that open
// and close in it. A code span is kept whole, "<!--" in it included, and a
// "<!--" that does not close is plain text. The "-->" may start two bytes into
// the "<!--", so "<!-->" and "<!--->" are whole comments, as the block rule
// reads them.
func uncommented(line string) string {
	var b strings.Builder
	for {
		open := strings.Index(line, "<!--")
		if open < 0 {
			break
		}
		if span := codeSpan.FindStringIndex(line); span != nil && span[0] < open {
			b.WriteString(line[:span[1]])
			line = line[span[1]:]
			continue
		}
		_, rest, closed := strings.Cut(line[open+2:], "-->")
		if !closed {
			break
		}
		b.WriteString(line[:open])
		line = rest
	}
	b.WriteString(line)
	return b.String()
}

// readmeFlagNames is the flags readmeFlags finds in readme, each with the
// heading of each row that names it, sorted.
func readmeFlagNames(t *testing.T, readme string) []string {
	t.Helper()
	flags, err := readmeFlags(readme)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for name, rows := range flags {
		for _, r := range rows {
			names = append(names, r.heading+": "+name)
		}
	}
	slices.Sort(names)
	return names
}

// flagTable is a "### Flags" reference that starts with a table under
// "#### Display" whose one row names --web.
const flagTable = "### Flags\n\n#### Display\n\n| Flag | Default | What it does |\n| --- | --- | --- |\n| `--web` | — | x |\n"

// tableHead starts a table of flags: its header row and its delimiter row.
const tableHead = "| Flag | Default | What it does |\n| --- | --- | --- |\n"

// TestReadmeFlagsLeaveOutAnUnclosedComment: rows in an HTML comment do not
// render. A line that starts with "<!--", with or without a space after it,
// hides itself and the lines after it, through the line that holds "-->" (a
// "->" ends nothing), or to the end when no line does; one that starts with
// "<!-" and no second dash hides nothing. The "-->" need not start its line,
// and that line may be indented by any amount (four or eight columns here),
// with text before or after the "-->"; a line that starts with "<!-->" closes
// on itself, so it hides only itself, a row after the "<!-->" included.
// Inside, a fence opens no code and a "#" line is no heading, not even a
// "### Flags" before the section. In a row's first cell, each comment closed
// in the cell is taken out, with the code spans in it; one not closed there is
// plain text and hides nothing, nor does "<!--" in any code span of the cell,
// the first or a later one, even with a "-->" after it in the cell; "<!-->"
// and "<!--->" are whole comments. A comment in a heading, the section's own
// included, is no part of the heading's name, and "<!-->" is a whole comment
// there too, so the heading "#### Peer <!-->x -->" reads "Peer x -->". md4c
// (md2html --github) and markdown-it-py (CommonMark with tables) show each
// input's rows as read here; for them to agree, a comment block comes after a
// blank line, and a new table after it.
func TestReadmeFlagsLeaveOutAnUnclosedComment(t *testing.T) {
	for _, tc := range []struct {
		name, readme string
		want         []string
	}{
		{"closed in a row or a heading", flagTable +
			"| `--window` | — | x | <!-- a note --> |\n| <!-- `-s` --> `--web-port PORT` | 6969 | x |\n" +
			"| <!--> `--web-addr ADDRESS` --> | 127.0.0.1 | x |\n| <!---> `--web-quality N` <!-- --> | 5 | x |\n" +
			"| <!-- a note --> `--window-size SIZE` <!-- `-s` --> | — | x |\n" +
			"\n#### Peer <!-- one row -->\n\n" + tableHead + "| `--peer NAME` | — | x |\n" +
			"\n#### Peer <!-->x -->\n\n" + tableHead + "| `--takeover` | — | x |\n",
			[]string{"Display: web", "Display: web-addr", "Display: web-port", "Display: web-quality", "Display: window", "Display: window-size", "Peer x -->: takeover", "Peer: peer"}},
		{"closed in the section's heading",
			"### Flags <!-- the reference -->\n\n#### Display\n\n" + tableHead + "| `--web` | — | x |\n",
			[]string{"Display: web"}},
		{"not closed in a row", flagTable +
			"| `--web-addr ADDRESS` | 127.0.0.1 | x <!-- left open |\n| <!-- left open `--window` | — | x |\n" +
			"\n#### Peer\n\n" + tableHead + "| `--peer NAME` | — | x |\n",
			[]string{"Display: web", "Display: web-addr", "Display: window", "Peer: peer"}},
		{"in a code span", flagTable +
			"| `--window` | — | `<!--` |\n| `--web-port PORT` `<!--` | 6969 | `-->` `--gone` |\n" +
			"| `--web-addr ADDRESS` | 127.0.0.1 | x |\n| `<!--` `--web-quality N` --> | 5 | x |\n" +
			"| `--keys` `<!--` `--takeover` --> | — | x |\n",
			[]string{"Display: keys", "Display: takeover", "Display: web", "Display: web-addr", "Display: web-port", "Display: web-quality", "Display: window"}},
		{"opened in one row and closed in the next", flagTable +
			"| `--window` | — | x <!-- a note |\n| `--web-port PORT` | 6969 | x --> |\n" +
			"| `--web-addr ADDRESS` | 127.0.0.1 | x |\n",
			[]string{"Display: web", "Display: web-addr", "Display: web-port", "Display: window"}},
		{"a block", flagTable +
			"\n<!-- one day:\nan arrow -> ends nothing\n| `--gone` | — | x |\nsoon --> | `--tail` | — | x |\n" + tableHead + "| `--window` | — | x |\n" +
			"\n   <!-- three spaces in --> | `--same` | — | x |\n" + tableHead + "| `--web-port PORT` | 6969 | x |\n" +
			"\n<!-->\n" + tableHead + "| `--web-addr ADDRESS` | 127.0.0.1 | x |\n" +
			"\n    <!-- four spaces in: code\n\n" + tableHead + "| `--web-quality N` | 5 | x |\n" +
			"\n<!-- old:\n```\n-->\n" + tableHead + "| `--takeover` | — | x |\n" +
			"\n<!-- old:\n## Not the end\n#### Hidden\n    -->\n" + tableHead + "| `--keys` | — | x |\n" +
			"\n<!-- old:\n    more -->\n" + tableHead + "| `--window-size SIZE` | — | x |\n" +
			"\n<!--old:\n| `--gone` | — | x |\n-->\n" + tableHead + "| `--serial SERIAL` | — | x |\n" +
			"\n<!- no comment >\n\n" + tableHead + "| `--peer NAME` | — | x |\n" +
			"\n<!-- note\n    more --> after\n" + tableHead + "| `--max-size N` | 1280 | x |\n" +
			"\n<!--> | `--gone` | — | x |\n\n" + tableHead + "| `--max-fps N` | 0 | x |\n" +
			"\n<!-- note\n        -->\n" + tableHead + "| `--audio-source NAME` | output | x |\n",
			[]string{"Display: audio-source", "Display: keys", "Display: max-fps", "Display: max-size", "Display: peer", "Display: serial", "Display: takeover", "Display: web", "Display: web-addr", "Display: web-port", "Display: web-quality", "Display: window", "Display: window-size"}},
		{"a block before the section", "<!--\n### Flags\n-->\n\n#### Display\n\n" + tableHead + "| `--gone` | — | x |\n\n" + flagTable,
			[]string{"Display: web"}},
		{"a block not closed", flagTable +
			"\n<!-- left open\n" + tableHead + "| `--window` | — | x |\n" +
			"\n#### Peer\n\n" + tableHead + "| `--peer NAME` | — | x |\n",
			[]string{"Display: web"}},
	} {
		if got := readmeFlagNames(t, tc.readme); !slices.Equal(got, tc.want) {
			t.Errorf("%s: read %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestReadmeFlagsEndACommentWithItsCell: GFM splits a row into cells at its
// pipes before it reads them, so a comment cannot go on past its cell: in the
// first cell, a "<!--" that does not close there is plain text, and a "-->"
// in a later cell closes nothing. markdown-it-py shows these rows. md4c,
// which reads a comment across the pipes, shows --gone, --gone-too and
// --gone-three in the first column too, and hides --web-port and
// --web-quality.
func TestReadmeFlagsEndACommentWithItsCell(t *testing.T) {
	readme := flagTable +
		"| `--window` <!-- | x | --> `--gone` | x |\n" +
		"| `--web-addr ADDRESS` <!-- `--hidden` --> <!-- | x | --> `--gone-too` | x |\n" +
		"| <!-- `--web-port PORT` | 6969 | --> | `--also-gone` |\n" +
		"| <!-- `--web-quality N` | 5 --> `--gone-three` | x |\n"
	want := []string{"Display: web", "Display: web-addr", "Display: web-port", "Display: web-quality", "Display: window"}
	if got := readmeFlagNames(t, readme); !slices.Equal(got, want) {
		t.Errorf("read %q, want %q", got, want)
	}
}

// TestReadmeFlagsLeaveOutFencedCode: three or more backticks or tildes,
// indented by three columns at most, with or without an info string after
// them (such as "sh", or "a~b" after tildes), open code, which only a fence of
// the same character, at least as long, ends: one indented by three columns at
// most, with only white space after it, such as a tab or the carriage return
// of a CRLF line ending. A blank line ends nothing, nor does a fence with text
// after it or one indented by four columns, a tab going on to the next
// multiple of four (" \t~~~"). Inside the code, a "#" line is no heading and
// ends nothing, not even a "### Flags" before the section, and "<!--" starts
// no comment. Two tildes, as in ~~struck~~ text, open no code. md4c and
// markdown-it-py show the same rows.
func TestReadmeFlagsLeaveOutFencedCode(t *testing.T) {
	readme := "~~~ md\n### Flags\n~~~\n\n```sh\n### Flags\n```\n\n#### Display\n\n" + tableHead + "| `--gone` | — | x |\n\n" + flagTable +
		"\n~~~\n| `--a` | — | x |\n```\n| `--b` | — | x |\n~~~\n" +
		"\n~~~~text\n| `--c` | — | x |\n~~~\n| `--d` | — | x |\n  ~~~~~ \n" +
		"\n```\n| `--e` | — | x |\n\n| `--f` | — | x |\n```\n" +
		"\n~~~\n~~~ text\n| `--g` | — | x |\n    ~~~\n| `--h` | — | x |\n~~~\n" +
		"\n" + tableHead + "| `--window` | — | x |\n" +
		"\n~~old~~ is gone.\n\n" + tableHead + "| `--web-port PORT` | 6969 | x |\n" +
		"\n```sh\n# a shell comment\n<!-- an HTML sample\n#### Not a heading\n```\n" +
		"\n~~~\n~~~x\n# not the end\n#### Not one either\n<!-- nor a comment\n~~~\n" +
		"\n~~~\n \t~~~\n" + tableHead + "| `--j` | — | x |\n~~~\n" +
		"\n~~~ a~b\n" + tableHead + "| `--k` | — | x |\n~~~\n" +
		"\n   ~~~\n| `--i` | — | x |\n   ~~~\n" +
		"\n" + tableHead + "| `--web-addr ADDRESS` | 127.0.0.1 | x |\n" +
		"\n~~~\n| `--l` | — | x |\n~~~\t\n\n" + tableHead + "| `--keys` | — | x |\n" +
		"\n~~~\n| `--m` | — | x |\n~~~\r\n\n" + tableHead + "| `--max-fps FPS` | — | x |\n"
	want := []string{"Display: keys", "Display: max-fps", "Display: web", "Display: web-addr", "Display: web-port", "Display: window"}
	if got := readmeFlagNames(t, readme); !slices.Equal(got, want) {
		t.Errorf("read %q, want %q", got, want)
	}
}

// TestReadmeFlagsLeaveOutIndentedCode: a line indented by four columns or
// more, a tab going on to the next multiple of four, is never a row, a heading
// (a "### Flags" there starts no section) or a fence. After a blank line or a
// heading it is code, up to the next line indented less; under a row it is
// code too, which ends the table; after a line of a paragraph it is more of
// the paragraph. Indented by three spaces, a row is the table's. md4c and
// markdown-it-py show the same rows.
func TestReadmeFlagsLeaveOutIndentedCode(t *testing.T) {
	readme := "Some text.\n\n    ### Flags\n\n#### Display\n\n" + tableHead + "| `--gone` | — | x |\n\n" + flagTable +
		"\n    | `--a` | — | x |\n\n    # not a heading\n\t#### Not one either\n    ```\n" +
		"\n" + tableHead + "   | `--window` | — | x |\n\t| `--web-port PORT` | 6969 | x |\n    | `--web-addr ADDRESS` | 127.0.0.1 | x |\n" +
		"\nA paragraph\n    | `--b` | — | x |\n    # not the end\n" +
		"\n#### Peer\n    | `--c` | — | x |\n\n" + tableHead + "| `--peer NAME` | — | x |\n"
	want := []string{"Display: web", "Display: window", "Peer: peer"}
	if got := readmeFlagNames(t, readme); !slices.Equal(got, want) {
		t.Errorf("read %q, want %q", got, want)
	}
}

// TestReadmeFlagsCountRowsAfterBackticksThatAreNoFence: three backticks with a
// backtick anywhere after them on the line, in the info string's first word or
// a later one, open no code (CommonMark: a backtick fence's info string has no
// backtick), so the rows after them count; three tildes with one after them do
// open code. md4c and markdown-it-py show the same rows.
func TestReadmeFlagsCountRowsAfterBackticksThatAreNoFence(t *testing.T) {
	readme := flagTable +
		"\n``` not`a fence\n\n" + tableHead + "| `--window` | — | x |\n" +
		"\n~~~ a`fence\n| `--a` | — | x |\n~~~\n\n" + tableHead + "| `--web-port PORT` | 6969 | x |\n" +
		"\n``` sh x`y\n\n" + tableHead + "| `--web-addr ADDRESS` | 127.0.0.1 | x |\n"
	want := []string{"Display: web", "Display: web-addr", "Display: web-port", "Display: window"}
	if got := readmeFlagNames(t, readme); !slices.Equal(got, want) {
		t.Errorf("read %q, want %q", got, want)
	}
}
