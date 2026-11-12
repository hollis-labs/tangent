package hostclient

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func seen(t *testing.T, client *http.Client, url string) string {
	t.Helper()
	res, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	return res.Header.Get("X-Seen")
}

// The key is read on every request: a renewal the host wrote to the file reaches the next call, and
// with no key configured no Authorization header is sent at all.
func TestTheKeyIsReadFromTheFileOnEveryRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Seen", r.Header.Get("Authorization"))
	}))
	defer server.Close()
	client := &http.Client{Transport: &keyTransport{base: http.DefaultTransport}}

	t.Setenv(KeyEnv, "")
	t.Setenv(KeyFileEnv, "")
	if got := seen(t, client, server.URL); got != "" {
		t.Fatalf("no key configured but Authorization = %q", got)
	}

	file := filepath.Join(t.TempDir(), "mcp-key")
	t.Setenv(KeyFileEnv, file)
	if err := os.WriteFile(file, []byte("first-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := seen(t, client, server.URL); got != "Bearer first-key" {
		t.Fatalf("Authorization = %q", got)
	}
	if err := os.WriteFile(file, []byte("second-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := seen(t, client, server.URL); got != "Bearer second-key" {
		t.Fatalf("after renewal Authorization = %q", got)
	}

	// A file the host named but that is gone is an error, not a silent request with no credential.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(server.URL); err == nil {
		t.Fatal("a missing key file was ignored")
	}
}
