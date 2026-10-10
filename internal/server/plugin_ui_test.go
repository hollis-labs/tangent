package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	driver "github.com/hollis-labs/libs/plugin-mcp/plugin-host"
	"github.com/hollis-labs/tangent/internal/pluginui"
)

func TestSealedUIResponsesAreExactAndUnmounted(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "retained")
	identity, err := pluginui.InitializeRetention(directory)
	if err != nil {
		t.Fatal(err)
	}
	retention, err := pluginui.OpenRetention(directory, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := retention.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	store, err := pluginui.NewStore("https://tangent.test", retention)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("export const ownedFixture = 1;")
	hash := sha256.Sum256(body)
	graph, err := pluginui.AdmitGraph(strings.Repeat("a", 64), []pluginui.Module{{ID: "plugin", Bytes: body, SHA256: hex.EncodeToString(hash[:])}}, nil, func([]pluginui.Module, []pluginui.Import) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	var live atomic.Bool
	live.Store(true)
	scope := strings.Repeat("b", 64)
	delivery, err := store.Provision(scope, pluginui.OwnerLease{Owner: driver.Owner{HostInstance: strings.Repeat("c", 64), OwnerID: "sample", OwnerGeneration: 1}, Current: live.Load}, graph, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	doc := pluginui.Document{FrameID: scope, HTML: "<!doctype html><p>generated fixture</p>", CSP: "script-src 'none'; object-src 'none'", PermissionsPolicy: "camera=()"}
	if sealDocumentErr := delivery.SealDocument(doc, func(pluginui.Document) error { return nil }); sealDocumentErr != nil {
		t.Fatal(sealDocumentErr)
	}
	handler := pluginUISealedHandler(store)
	request := func(method, target string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
		return recorder
	}
	module := delivery.URLs()["plugin"]
	response := request(http.MethodGet, module)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), body) {
		t.Fatal("module response changed", response.Code)
	}
	for key, value := range map[string]string{"Content-Type": "text/javascript", "X-Content-Type-Options": "nosniff", "Cache-Control": "no-store", "Access-Control-Allow-Origin": "*", "Timing-Allow-Origin": "*"} {
		if response.Header().Get(key) != value {
			t.Fatal("missing actual module policy", key)
		}
	}
	if response.Header().Get("Access-Control-Allow-Credentials") != "" || response.Header().Get("Location") != "" {
		t.Fatal("credentials or redirect added")
	}
	document := request(http.MethodGet, delivery.DocumentURL())
	if document.Code != http.StatusOK || document.Body.String() != doc.HTML || document.Header().Get("Content-Security-Policy") != doc.CSP || document.Header().Get("Permissions-Policy") != doc.PermissionsPolicy {
		t.Fatal("generated document policy changed")
	}
	if request(http.MethodPost, module).Code != http.StatusMethodNotAllowed {
		t.Fatal("write request admitted")
	}
	if request(http.MethodGet, module+"?sourceUrl=changed").Code != http.StatusNotFound {
		t.Fatal("mutable URL alias admitted")
	}
	parsed, err := url.Parse(module)
	if err != nil {
		t.Fatal(err)
	}
	if request(http.MethodGet, strings.Replace(parsed.Path, "sample", "other", 1)).Code != http.StatusNotFound {
		t.Fatal("foreign owner path admitted")
	}
	live.Store(false)
	if request(http.MethodGet, module).Code != http.StatusNotFound {
		t.Fatal("ended owner served")
	}
	if releaseErr := delivery.Release(context.Background()); releaseErr != nil {
		t.Fatal(releaseErr)
	}
}
