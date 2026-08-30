// Package fakegithub serves a fake GitHub Releases API plus asset download
// endpoints for update-flow tests, mirroring the real API's payload shape.
package fakegithub

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// New starts a server that answers <base>/repos/<owner>/<repo>/releases/latest
// with a release carrying one asset per files entry; each asset downloads
// from <base>/download/<name>. A nil files map still yields a valid release
// payload with an empty asset list.
func New(t *testing.T, tag string, files map[string][]byte) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			assets := make([]map[string]any, 0, len(files))
			for name := range files {
				assets = append(assets, map[string]any{
					"name":                 name,
					"browser_download_url": server.URL + "/download/" + name,
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": tag,
				"html_url": server.URL + "/releases/tag/" + tag,
				"assets":   assets,
			})
			return
		}
		if data, ok := files[strings.TrimPrefix(r.URL.Path, "/download/")]; ok {
			_, _ = w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

// Zip packs one file into an in-memory zip archive.
func Zip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("fakegithub: zip create: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("fakegithub: zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("fakegithub: zip close: %v", err)
	}
	return buf.Bytes()
}

// Checksums renders one `sha256  filename` line in the format the release
// checksums asset uses.
func Checksums(name string, data []byte) []byte {
	return []byte(fmt.Sprintf("%x  %s\n", sha256.Sum256(data), name))
}
