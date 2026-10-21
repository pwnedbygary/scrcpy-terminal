package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		candidate, current string
		want               bool
	}{
		{"2.1.0", "2.0.0", true},
		{"v2.0.10", "2.0.9", true},
		{"3.0.0", "2.9.9", true},
		{"2.0.0", "2.0.0", false},
		{"2.0.0", "2.1.0", false},
		{"2.1.0", "dev", false},
		{"dev", "2.1.0", false},
		{"2.1", "2.0.0", false},
		{"2.1.0-rc1", "2.0.0", false},
	} {
		if got := Newer(c.candidate, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.candidate, c.current, got)
		}
	}
}

// server publishes one release whose "scterm" asset has the given bytes and
// advertised size and digest.
func server(t *testing.T, body []byte, size int64, digest string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			if r.Header.Get("User-Agent") == "" {
				http.Error(w, "GitHub requires a User-Agent", http.StatusForbidden)
				return
			}
			fmt.Fprintf(w, `{"tag_name":"v2.1.0","html_url":"https://example.invalid/v2.1.0","body":"Notes",
				"assets":[{"name":"scterm","browser_download_url":"%s/scterm","size":%d,"digest":%q},
				          {"name":"scterm-android-2.1.0.apk","browser_download_url":"%s/apk","size":3}]}`, srv.URL, size, digest, srv.URL)
		case "/scterm":
			w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestLatestAndDownload(t *testing.T) {
	body := []byte("the new scterm")
	srv := server(t, body, int64(len(body)), "sha256:"+strings.ToUpper(sha(body)))
	r, err := Latest(context.Background(), srv.Client(), srv.URL+"/latest", "scterm-test")
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != "2.1.0" || r.Notes != "Notes" || len(r.Assets) != 2 {
		t.Fatalf("release %+v", r)
	}
	if a := r.Asset("scterm-android-2.1.0.apk"); a == nil || a.SHA256 != "" {
		t.Fatalf("apk asset %+v", a)
	}
	a := r.Asset("scterm")
	if a == nil || a.SHA256 != sha(body) {
		t.Fatalf("binary asset %+v", a)
	}
	dir := t.TempDir()
	path, err := Download(context.Background(), srv.Client(), a, dir, "scterm-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(body) {
		t.Fatalf("downloaded %q", got)
	}
	if r.Asset("nope") != nil {
		t.Error("found an asset that does not exist")
	}
}

func TestDownloadRefusesWhatWasNotPublished(t *testing.T) {
	body := []byte("the new scterm")
	for name, srv := range map[string]*httptest.Server{
		"wrong digest": server(t, body, int64(len(body)), "sha256:"+sha([]byte("something else"))),
		"short":        server(t, body, int64(len(body))+5, ""),
		"long":         server(t, body, int64(len(body))-1, ""),
	} {
		r, err := Latest(context.Background(), srv.Client(), srv.URL+"/latest", "scterm-test")
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		if _, err := Download(context.Background(), srv.Client(), r.Asset("scterm"), dir, "scterm-test", nil); err == nil {
			t.Errorf("%s: downloaded anyway", name)
		}
		if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 0 {
			t.Errorf("%s: left %v behind", name, left)
		}
	}
	srv := server(t, body, 0, "")
	if _, err := Latest(context.Background(), srv.Client(), srv.URL+"/missing", "scterm-test"); err == nil {
		t.Error("a 404 read as a release")
	}
}

func TestDownloadReportsProgress(t *testing.T) {
	body := bytes.Repeat([]byte("scterm "), 40000) // 280 kB: several reads
	for name, size := range map[string]int64{"stated size": int64(len(body)), "no stated size": 0} {
		srv := server(t, body, size, "sha256:"+sha(body))
		r, err := Latest(context.Background(), srv.Client(), srv.URL+"/latest", "scterm-test")
		if err != nil {
			t.Fatal(err)
		}
		var seen []int64
		if _, err := Download(context.Background(), srv.Client(), r.Asset("scterm"), t.TempDir(), "scterm-test", func(done int64) {
			seen = append(seen, done)
		}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(seen) < 2 || seen[len(seen)-1] != int64(len(body)) {
			t.Errorf("%s: progress %v, want several reports ending at %d", name, seen, len(body))
		}
		for i := 1; i < len(seen); i++ {
			if seen[i] <= seen[i-1] {
				t.Errorf("%s: progress went from %d to %d", name, seen[i-1], seen[i])
			}
		}
	}
}
