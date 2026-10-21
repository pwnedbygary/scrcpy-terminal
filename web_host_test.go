package main

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A website can point a name of its own at this computer (DNS rebinding). Its
// page, loaded from that name, then reaches the server as if it were the
// website, and every request carries the website's name in its Host header,
// with an Origin to match. The server serves only requests addressed to a name
// it is meant to be reached by.

// askRaw sends the server at addr one raw request and returns the response and
// its body (empty after a WebSocket upgrade or for HEAD, which have none).
func askRaw(t *testing.T, addr, request string) (*http.Response, string) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatalf("write: %v", err)
	}
	first, _, _ := strings.Cut(request, "\r\n")
	method, _, _ := strings.Cut(first, " ")
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: method})
	if err != nil {
		t.Fatalf("%s: %v", first, err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s: body: %v", first, err)
	}
	return resp, string(body)
}

// askAs asks the server at addr for path with exactly the Host header host, an
// Origin header when origin is not empty, and a WebSocket upgrade when upgrade
// is set.
func askAs(t *testing.T, addr, path, host, origin string, upgrade bool) (*http.Response, string) {
	t.Helper()
	req := "GET " + path + " HTTP/1.1\r\nHost: " + host + "\r\n"
	if origin != "" {
		req += "Origin: " + origin + "\r\n"
	}
	if upgrade {
		req += "Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\n" +
			"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString([]byte("scterm-test-key")) + "\r\n"
	} else {
		req += "Connection: close\r\n"
	}
	return askRaw(t, addr, req+"\r\n")
}

// checkRefused checks the answer to a request the server must refuse: a 403
// with a short line of plain text that names SCTERM_WEB_HOSTS, and none of the
// page.
func checkRefused(t *testing.T, what string, resp *http.Response, body string) {
	t.Helper()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("%s: status %d, want 403", what, resp.StatusCode)
		return
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("%s: content type %q, want plain text", what, ct)
	}
	if !strings.Contains(body, "SCTERM_WEB_HOSTS") || len(body) > 300 || strings.ContainsAny(body, "<>") {
		t.Errorf("%s: the refusal is not a short plain line naming SCTERM_WEB_HOSTS: %.300q", what, body)
	}
}

// reportsOf returns the notices held since startNotices that mention name, and
// how many times they came in all.
func reportsOf(name string) (lines []string, times int) {
	notices.mu.Lock()
	defer notices.mu.Unlock()
	for _, l := range notices.replay {
		if strings.Contains(l.text, name) {
			lines = append(lines, l.text)
			times += l.count
		}
	}
	return lines, times
}

// machineName is this computer's host name as a browser writes it in an
// address: in lower case, here without .local.
func machineName(t *testing.T) string {
	t.Helper()
	name, err := os.Hostname()
	if err != nil {
		t.Fatalf("os.Hostname: %v", err)
	}
	return strings.TrimSuffix(strings.ToLower(name), ".local")
}

// TestWebRefusesARebindingName: a page loaded from a website's name that now
// points at this computer gets nothing from the server, though its Origin
// matches its Host: not the page, not an asset, and no WebSocket. It gets a
// 403 that says to open the page by the address it reached, or to name it in
// SCTERM_WEB_HOSTS, and the name is reported once, however often it comes.
func TestWebRefusesARebindingName(t *testing.T) {
	t.Setenv(webHostsEnv, "")
	startNotices(t)
	h := newWebTestHarness(t, config{web: true}, false)
	host := "evil.example:" + strconv.Itoa(h.srv.port)
	open := "http://" + h.url() + "/" // the address the requests reach
	for _, c := range []struct {
		path    string
		upgrade bool
	}{{"/", false}, {"/player.js", false}, {"/pcm-worklet.js", false}, {"/ws", true}, {"/ws", true}, {"/", false}} {
		what := fmt.Sprintf("GET %s as %s", c.path, host)
		resp, body := askAs(t, h.url(), c.path, host, "http://"+host, c.upgrade)
		checkRefused(t, what, resp, body)
		if resp.StatusCode == http.StatusForbidden && !strings.Contains(body, open) {
			t.Errorf("%s: the refusal does not say to open %s: %q", what, open, body)
		}
	}
	if n := h.srv.clientCount(); n != 0 {
		t.Errorf("%d refused WebSocket handshakes became viewers", n)
	}
	lines, times := reportsOf("evil.example")
	if len(lines) != 1 || times != 1 {
		t.Fatalf("the refused name was reported %d times in %d lines, want once: %q", times, len(lines), lines)
	}
	if !strings.Contains(lines[0], open) || !strings.Contains(lines[0], "SCTERM_WEB_HOSTS") {
		t.Errorf("the report does not say to use %s or SCTERM_WEB_HOSTS: %q", open, lines[0])
	}
}

// serveAt runs a web server with no device that listens on addr, on a free
// port, or skips the test when addr cannot be listened on here.
func serveAt(t *testing.T, addr string) *webServer {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(addr, "0"))
	if err != nil {
		t.Skipf("cannot listen on %s here: %v", addr, err)
	}
	ln.Close()
	cfg := config{web: true, webAddr: addr}
	srv := newWebServer(cfg, &session{deviceName: "testphone"}, &streamState{cfg: cfg}, nil, nil, nil)
	go func() { _ = srv.run(false) }()
	select {
	case <-srv.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("web server never started listening")
	}
	t.Cleanup(srv.stop)
	return srv
}

// TestWebServesTheAddressItPrints: the address scterm prints, which --window
// opens, is served: 127.0.0.1 by default, and with --web-addr localhost or ::1.
func TestWebServesTheAddressItPrints(t *testing.T) {
	for _, addr := range []string{"127.0.0.1", "localhost", "::1"} {
		t.Run(addr, func(t *testing.T) {
			srv := serveAt(t, addr)
			resp, err := http.Get(srv.url)
			if err != nil {
				t.Fatalf("GET %s: %v", srv.url, err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `<canvas id="screen"`) {
				t.Errorf("GET %s: status %d, want the page", srv.url, resp.StatusCode)
			}
		})
	}
}

// addrListener is a listener that reports addr as its address, whatever
// address it listens on.
type addrListener struct {
	net.Listener
	addr net.Addr
}

func (l addrListener) Addr() net.Addr { return l.addr }

// TestWebServesTheNamesItStartsWith: when --web-addr is a name, the server
// serves the address it prints, and that name on any port; and when it listens
// on an address on the network, it serves this computer's host name, with and
// without .local. Names around them stay refused. Here it listens on 127.0.0.1
// whatever address it asks for, and is told that it listens on 192.0.2.10, an
// address on the network, so no name is looked up: the test needs no DNS and
// no /etc/hosts. SCTERM_WEB_HOSTS is emptied, so that a developer's own list
// cannot add names.
func TestWebServesTheNamesItStartsWith(t *testing.T) {
	t.Setenv(webHostsEnv, "")
	listen, hostname := webListen, osHostname
	t.Cleanup(func() { webListen, osHostname = listen, hostname })
	var asked string
	webListen = func(network, address string) (net.Listener, error) {
		asked = address
		ln, err := listen(network, "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		return addrListener{ln, &net.TCPAddr{IP: net.IPv4(192, 0, 2, 10), Port: ln.Addr().(*net.TCPAddr).Port}}, nil
	}
	osHostname = func() (string, error) { return "Scterm-Box", nil }
	startNotices(t)
	cfg := config{web: true, webAddr: "Mirror.Scterm.test"}
	srv := newWebServer(cfg, &session{deviceName: "testphone"}, &streamState{cfg: cfg}, nil, nil, nil)
	go func() { _ = srv.run(false) }()
	select {
	case <-srv.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("web server never started listening")
	}
	t.Cleanup(srv.stop)
	port := strconv.Itoa(srv.port)
	if asked != "Mirror.Scterm.test:0" {
		t.Errorf("the server asked to listen on %q, want Mirror.Scterm.test:0", asked)
	}
	printed, err := url.Parse(srv.url)
	if err != nil || printed.Host != "Mirror.Scterm.test:"+port {
		t.Fatalf("the server prints %q (%v), want the address http://Mirror.Scterm.test:%s/", srv.url, err, port)
	}
	addr := net.JoinHostPort("127.0.0.1", port)
	for _, host := range []string{printed.Host, "mirror.scterm.test", "scterm-box:" + port, "Scterm-Box.local:" + port} {
		resp, body := askAs(t, addr, "/", host, "", false)
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, `<canvas id="screen"`) {
			t.Errorf("GET / as %q: status %d, want the page", host, resp.StatusCode)
		}
	}
	for _, host := range []string{"www.mirror.scterm.test:" + port, "scterm.test:" + port, "other-box.local:" + port} {
		resp, body := askAs(t, addr, "/", host, "", false)
		checkRefused(t, "GET / as "+host, resp, body)
	}
}

// TestWebRefusalSuggestsTheAddressReached: the address a refusal suggests is
// the one the request reached, so one the server listens on: here 127.0.0.2,
// not 127.0.0.1.
func TestWebRefusalSuggestsTheAddressReached(t *testing.T) {
	t.Setenv(webHostsEnv, "")
	startNotices(t)
	srv := serveAt(t, "127.0.0.2")
	addr := net.JoinHostPort("127.0.0.2", strconv.Itoa(srv.port))
	resp, body := askAs(t, addr, "/", "evil.example", "", false)
	checkRefused(t, "GET / as evil.example", resp, body)
	if open := "http://" + addr + "/"; resp.StatusCode == http.StatusForbidden && !strings.Contains(body, open) {
		t.Errorf("the refusal does not say to open %s: %q", open, body)
	}
}

// TestWebServesItsOwnNames: the page is served to a request addressed by an IP
// address (any, IPv6 in brackets, with a zone or without), localhost or a name
// that ends in .localhost, or a name SCTERM_WEB_HOSTS lists, in any case, with
// a final dot or not, on any port or none (an empty port included); so are an
// asset and the WebSocket. None of these requests is reported as refused.
func TestWebServesItsOwnNames(t *testing.T) {
	t.Setenv("SCTERM_WEB_HOSTS", " Mirror.Example.lan,,scterm.example.org:8443 , ")
	startNotices(t)
	h := newWebTestHarness(t, config{web: true}, false)
	port := strconv.Itoa(h.srv.port)
	names := []string{
		"127.0.0.1", "[::1]", "192.168.1.20", "10.0.0.7", "[fe80::1]", "[fe80::1%25eth0]", "[2001:db8::5]",
		"[::ffff:192.168.1.20]", "localhost", "LocalHost", "localhost.", "scterm.localhost", "a.b.LOCALHOST",
		"mirror.example.lan", "MIRROR.example.LAN", "scterm.example.org",
	}
	hosts := []string{"localhost:", "[::1]:"}
	for _, name := range names {
		hosts = append(hosts, name, name+":"+port, name+":80")
	}
	for _, host := range hosts {
		resp, body := askAs(t, h.url(), "/", host, "", false)
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, `<canvas id="screen"`) {
			t.Errorf("GET / as %q: status %d, want the page", host, resp.StatusCode)
		}
	}
	for _, name := range []string{"127.0.0.1", "[::1]", "192.168.1.20", "localhost", "scterm.localhost",
		"mirror.example.lan"} {
		host := name + ":" + port
		if resp, _ := askAs(t, h.url(), "/player.js", host, "", false); resp.StatusCode != http.StatusOK {
			t.Errorf("GET /player.js as %q: status %d, want 200", host, resp.StatusCode)
		}
		if resp, _ := askAs(t, h.url(), "/ws", host, "http://"+host, true); resp.StatusCode != http.StatusSwitchingProtocols {
			t.Errorf("WebSocket as %q: status %d, want 101", host, resp.StatusCode)
		}
	}
	if lines, _ := reportsOf("refused"); len(lines) != 0 {
		t.Errorf("served requests were reported as refused: %q", lines)
	}
}

// TestWebLoopbackRefusesThisComputersName: on a loopback address, the default,
// no other computer reaches the server, and this one reaches it by an address
// or localhost, so this computer's name is refused there, with or without
// .local, like any other name: someone on the network who answers for that
// name could point it at 127.0.0.1 and serve a page of their own under it.
func TestWebLoopbackRefusesThisComputersName(t *testing.T) {
	t.Setenv(webHostsEnv, "") // a developer's own list could name this computer
	startNotices(t)
	h := newWebTestHarness(t, config{web: true}, false)
	machine := machineName(t)
	if _, err := netip.ParseAddr(machine); err == nil || machine == "" || machine == "localhost" ||
		strings.HasSuffix(machine, ".localhost") {
		t.Skipf("this computer's name, %q, is served as an address or localhost", machine)
	}
	osName, _ := os.Hostname()
	port := strconv.Itoa(h.srv.port)
	hosts := []string{osName, machine, machine + ":" + port, strings.ToUpper(machine) + ".", machine + ".local",
		machine + ".local:" + port}
	if first, _, dotted := strings.Cut(machine, "."); dotted {
		hosts = append(hosts, first+".local:"+port)
	}
	for _, host := range hosts {
		resp, body := askAs(t, h.url(), "/", host, "", false)
		checkRefused(t, fmt.Sprintf("GET / as %q", host), resp, body)
	}
	host := machine + ".local:" + port
	resp, body := askAs(t, h.url(), "/ws", host, "http://"+host, true)
	checkRefused(t, "WebSocket as "+host, resp, body)
	if n := h.srv.clientCount(); n != 0 {
		t.Errorf("%d refused WebSocket handshakes became viewers", n)
	}
}

// TestWebWarnsOfHostsEntriesItIgnores: each entry of SCTERM_WEB_HOSTS that no
// request can match is reported once, when the server starts, as ignored, with
// what to write instead: an entry that is not a name, or a name and a port (a
// URL, a pattern, a path, or an international name, which goes in its xn--
// form), and a name that starts with a dot or has two dots in a row (an entry
// that would not be a name even without its extra dots gets the first advice).
// The entry is quoted in ASCII, so a letter that only looks like an ASCII one
// shows as an escape. The entries that are names are served, and the ignored
// ones are not.
func TestWebWarnsOfHostsEntriesItIgnores(t *testing.T) {
	t.Setenv("SCTERM_WEB_HOSTS", "http://phone.lan, mirror.example.lan,*.example.lan,mirror.example.lan/scterm,"+
		".example.lan,box..example.lan,box.example.lan..:8443,*..example.lan,b\u00fccher.lan,\u212aitchen.lan")
	startNotices(t)
	h := newWebTestHarness(t, config{web: true}, false)
	const notAName = "an entry must be a name, or a name and a port, such as mirror.lan or mirror.lan:8443"
	const noMatch = "no request can match a name that starts with a dot or has two dots in a row; " +
		"list each name in full, such as mirror.example.lan"
	const international = notAName + "; write an international name in its xn-- form"
	reported := map[string]string{
		`"http://phone.lan"`:          notAName,
		`"*.example.lan"`:             notAName,
		`"mirror.example.lan/scterm"`: notAName,
		`".example.lan"`:              noMatch,
		`"box..example.lan"`:          noMatch,
		`"box.example.lan..:8443"`:    noMatch,
		`"*..example.lan"`:            notAName,
		`"b\u00fccher.lan"`:           international,
		`"\u212aitchen.lan"`:          international,
	}
	checkReported := func(when string) {
		t.Helper()
		for entry, why := range reported {
			want := "scterm: web: ignored " + entry + " in SCTERM_WEB_HOSTS: " + why
			if lines, times := reportsOf("ignored " + entry + " "); len(lines) != 1 || times != 1 || lines[0] != want {
				t.Errorf("%s: SCTERM_WEB_HOSTS entry %s was reported %d times in %q, want once, as %q",
					when, entry, times, lines, want)
			}
		}
	}
	checkReported("at start")
	if resp, _ := askAs(t, h.url(), "/", "mirror.example.lan", "", false); resp.StatusCode != http.StatusOK {
		t.Errorf("GET / as mirror.example.lan: status %d, want 200", resp.StatusCode)
	}
	for _, host := range []string{"phone.lan", "www.example.lan", "example.lan", ".example.lan", "box..example.lan",
		"box.example.lan..", "kitchen.lan", "xn--bcher-kva.lan"} {
		resp, body := askAs(t, h.url(), "/", host, "", false)
		checkRefused(t, "GET / as "+host, resp, body)
	}
	checkReported("after requests")
	if lines, _ := reportsOf(`ignored "mirror.example.lan"`); len(lines) != 0 {
		t.Errorf("a SCTERM_WEB_HOSTS entry that is a name was reported: %q", lines)
	}
}

// TestWebRefusesOtherNames: names that only look like allowed ones (another
// computer's .local name and a second final dot included), this computer's
// name under another domain, names around one SCTERM_WEB_HOSTS lists, and a
// Host header that names nothing readable (none, empty, a bare port, an IPv6
// address without brackets, brackets around anything else, a broken bracket
// or port) are refused like the rebinding name.
func TestWebRefusesOtherNames(t *testing.T) {
	t.Setenv("SCTERM_WEB_HOSTS", "mirror.example.lan")
	startNotices(t)
	h := newWebTestHarness(t, config{web: true}, false)
	port := strconv.Itoa(h.srv.port)
	hosts := []string{
		"evil.example", "evil.example.", "localhost.evil.example:" + port, "evillocalhost:" + port,
		"localhost-evil.example:" + port, "localhost.example:" + port, "other-host.local:" + port, "localhost..:" + port,
		"example.lan:" + port, "other.example.lan:" + port, "www.mirror.example.lan:" + port,
		"", ":" + port, "::1", "[::1", "[::1]x", "[127.0.0.1]:" + port,
		"[mirror.example.lan]:" + port, "localhost:http", "localhost:" + port + ":" + port,
	}
	if machine := machineName(t); machine != "localhost" && !strings.HasSuffix(machine, ".localhost") && machine != "" {
		hosts = append(hosts, machine+".evil.example:"+port, "www."+machine+":"+port, machine+".lan:"+port)
	}
	for _, host := range hosts {
		resp, body := askAs(t, h.url(), "/", host, "", false)
		checkRefused(t, fmt.Sprintf("GET / as %q", host), resp, body)
	}
	resp, body := askRaw(t, h.url(), "GET / HTTP/1.0\r\n\r\n") // HTTP/1.0 may leave Host out
	checkRefused(t, "GET / without a Host header", resp, body)
}

// TestWebReportsEachRefusedNameOnce: each refused name is reported once, and a
// page that tries many names cannot print a line for each: the first 16 are
// reported, then a single line says the rest go unreported.
func TestWebReportsEachRefusedNameOnce(t *testing.T) {
	startNotices(t)
	h := newWebTestHarness(t, config{web: true}, false)
	const tried, reported = 40, 16
	for range 2 {
		for i := range tried {
			resp, body := askAs(t, h.url(), "/", fmt.Sprintf("name-%02d.example", i), "", false)
			checkRefused(t, fmt.Sprintf("GET / as name-%02d.example", i), resp, body)
		}
	}
	for i := range tried {
		name, want := fmt.Sprintf("name-%02d.example", i), 0
		if i < reported {
			want = 1
		}
		if lines, times := reportsOf(`"` + name + `"`); times != want {
			t.Errorf("%s was reported %d times, want %d: %q", name, times, want, lines)
		}
	}
	if more, times := reportsOf("more names"); len(more) != 1 || times != 1 {
		t.Errorf("the line that says further names go unreported came %d times in %q, want once", times, more)
	}
}

// TestWebReportsANameOnceInAnyForm: a refused name is reported once, whatever
// its case, final dot or port.
func TestWebReportsANameOnceInAnyForm(t *testing.T) {
	t.Setenv(webHostsEnv, "")
	startNotices(t)
	h := newWebTestHarness(t, config{web: true}, false)
	for _, host := range []string{"evil.example", "Evil.Example", "evil.example.", "EVIL.EXAMPLE:1",
		"evil.example:" + strconv.Itoa(h.srv.port)} {
		resp, body := askAs(t, h.url(), "/", host, "", false)
		checkRefused(t, fmt.Sprintf("GET / as %q", host), resp, body)
	}
	if lines, times := reportsOf("refused"); len(lines) != 1 || times != 1 || !strings.Contains(lines[0], `"evil.example"`) {
		t.Errorf("evil.example in five forms was reported %d times in %q, want once, as \"evil.example\"", times, lines)
	}
}

// TestWebReportCutsALongName: a refused name longer than any DNS name can be
// (253 bytes) is reported cut there, with an ellipsis.
func TestWebReportCutsALongName(t *testing.T) {
	startNotices(t)
	h := newWebTestHarness(t, config{web: true}, false)
	long := strings.Repeat("a", 300) + ".example"
	resp, body := askAs(t, h.url(), "/", long, "", false)
	checkRefused(t, "GET / as a 308-byte name", resp, body)
	if lines, _ := reportsOf("refused"); len(lines) != 1 || !strings.Contains(lines[0], `"`+strings.Repeat("a", 253)+`…"`) {
		t.Errorf("a 308-byte name was not reported cut after 253 bytes: %q", lines)
	}
}

// TestWebRefusesEveryMethod: a request addressed to another name is refused
// whatever its method, not only GET.
func TestWebRefusesEveryMethod(t *testing.T) {
	t.Setenv(webHostsEnv, "")
	startNotices(t)
	h := newWebTestHarness(t, config{web: true}, false)
	for _, method := range []string{"HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
		what := method + " / as evil.example"
		resp, body := askRaw(t, h.url(), method+" / HTTP/1.1\r\nHost: evil.example\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		if method == "HEAD" { // no body to check
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("%s: status %d, want 403", what, resp.StatusCode)
			}
			continue
		}
		checkRefused(t, what, resp, body)
	}
}

// TestWebHostsVariableIsDocumented: SCTERM_WEB_HOSTS is under Environment in
// scterm -h and in the README's table of environment variables.
func TestWebHostsVariableIsDocumented(t *testing.T) {
	_, _, entries := readHelp(t)
	inHelp := false
	for _, e := range entries {
		if e.heading == "Environment" && strings.HasPrefix(strings.TrimSpace(e.lines[0]), "SCTERM_WEB_HOSTS ") {
			inHelp = true
		}
	}
	if !inHelp {
		t.Error("scterm -h does not list SCTERM_WEB_HOSTS under Environment")
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, table, _ := strings.Cut(string(readme), "\n### Environment variables\n")
	table, _, _ = strings.Cut(table, "\n#")
	if !strings.Contains(table, "\n| `SCTERM_WEB_HOSTS` |") {
		t.Error("README.md does not list SCTERM_WEB_HOSTS under ### Environment variables")
	}
}
