// Package release finds scterm's published releases on GitHub and downloads
// their files, checked against the digests GitHub publishes for them.
package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// LatestURL is the GitHub API endpoint for the newest published release.
const LatestURL = "https://api.github.com/repos/pwnedbygary/scrcpy-terminal/releases/latest"

// maxDownload bounds a download whose size GitHub did not state.
const maxDownload = 512 << 20

// Release is one published release.
type Release struct {
	Version string // the tag without its "v", e.g. "2.1.0"
	Notes   string
	Page    string
	Assets  []Asset
}

// Asset is one file attached to a release.
type Asset struct {
	Name   string
	URL    string
	Size   int64
	SHA256 string // hex; empty when GitHub published no digest
}

// Latest fetches the newest published release from url (LatestURL, or a
// test server speaking the same JSON).
func Latest(ctx context.Context, client *http.Client, url, userAgent string) (*Release, error) {
	resp, err := get(ctx, client, url, userAgent, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Body    string `json:"body"`
		Assets  []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("reading the release list: %w", err)
	}
	if raw.TagName == "" {
		return nil, fmt.Errorf("%s names no release", url)
	}
	r := &Release{Version: strings.TrimPrefix(raw.TagName, "v"), Notes: raw.Body, Page: raw.HTMLURL}
	for _, a := range raw.Assets {
		digest, _ := strings.CutPrefix(a.Digest, "sha256:")
		if digest == a.Digest {
			digest = "" // absent, or an algorithm this does not check
		}
		r.Assets = append(r.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size, SHA256: strings.ToLower(digest)})
	}
	return r, nil
}

// Asset returns the release's file named name, or nil.
func (r *Release) Asset(name string) *Asset {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i]
		}
	}
	return nil
}

// Newer reports whether candidate is a later version than current. Both are
// MAJOR.MINOR.PATCH with an optional "v"; anything else, such as a
// development build's "dev", compares as not newer.
func Newer(candidate, current string) bool {
	c, ok := parse(candidate)
	v, ok2 := parse(current)
	if !ok || !ok2 {
		return false
	}
	for i := range c {
		if c[i] != v[i] {
			return c[i] > v[i]
		}
	}
	return false
}

func parse(version string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Download saves a into a new file in dir, checking its size and digest,
// and returns the file's path. Nothing is left behind on failure.
func Download(ctx context.Context, client *http.Client, a *Asset, dir, userAgent string) (string, error) {
	f, err := os.CreateTemp(dir, ".scterm-download-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	fail := func(err error) (string, error) {
		f.Close()
		os.Remove(path)
		return "", err
	}
	resp, err := get(ctx, client, a.URL, userAgent, "application/octet-stream")
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()
	limit := int64(maxDownload)
	if a.Size > 0 {
		limit = a.Size + 1
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, limit))
	if err != nil {
		return fail(fmt.Errorf("downloading %s: %w", a.Name, err))
	}
	if a.Size > 0 && n != a.Size {
		return fail(fmt.Errorf("%s: got %d bytes, expected %d", a.Name, n, a.Size))
	}
	if got := hex.EncodeToString(h.Sum(nil)); a.SHA256 != "" && got != a.SHA256 {
		return fail(fmt.Errorf("%s: digest %s does not match the published %s", a.Name, got, a.SHA256))
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func get(ctx context.Context, client *http.Client, url, userAgent, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return resp, nil
}
