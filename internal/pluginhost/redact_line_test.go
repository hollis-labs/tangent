package pluginhost

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestRedactionPreservesCausesAcrossCredentialLines(t *testing.T) {
	raw := "starting\nTOKEN=S3CR01\nIMPORTANT CAUSE: database migration 42 failed\nKEY=S3CR02\nFINAL CAUSE: schema migration failed\n"
	first := redactRawDiagnostic(raw)
	if !strings.Contains(first, "\nIMPORTANT CAUSE") {
		t.Fatal("raw line boundaries lost", first)
	}
	wrapped := "pluginhost: load/init: plugin is gone\nplugin stderr:\n" + first
	twice := redactPluginDiagnostic(wrapped)
	for _, cause := range []string{"plugin is gone", "IMPORTANT CAUSE: database migration 42 failed", "FINAL CAUSE: schema migration failed"} {
		if !strings.Contains(twice, cause) {
			t.Errorf("cause lost: %s: %s", cause, twice)
		}
	}
	if strings.Contains(twice, "S3CR") {
		t.Fatal("secret leaked", twice)
	}
}

func TestRedactionPreservesOrdinaryNamesAndEncoding(t *testing.T) {
	for _, line := range []string{
		`level=ERROR msg="sync failed" project_key=TQ err="connection refused"`,
		"monkey=1 err=boom", "author=bob status=500 body=upstream timeout", "oauth_provider=github status=500 reason=rate limited",
		"keyspace=main; error: connection refused", "using keyring=default then failed: permission denied", "tokenizer=bpe loaded; next step failed: OOM",
		"progress 100%25 done; GET /api?q=%41%42", "path=/tmp/a%2Fb missing", "--auth-mode basic failed: unsupported", "--keyfile /etc/x.pem: no such file",
		`"monkey": 5, "error": "boom"`, `{"authority": "ca", "error": "cert expired"}`, "cache://host@region unreachable",
	} {
		if got := redactPluginDiagnostic(line); got != line {
			t.Errorf("diagnostic rewritten: %q => %q", line, got)
		}
	}
}

func TestRedactionAdditionalCredentialForms(t *testing.T) {
	for _, line := range []string{
		"token\u00a0=\u00a0S3CR31", "Authorization:Bearer\tS3CR37", "cookie: session=S3CR34", "Set-Cookie: sid=S3CR35",
		"jwt=S3CR38", "pwd=S3CR39", "PGPASSWORD=S3CR40", "DSN=user:S3CR36@tcp(db:3306)/x",
		"token=S3CR41%0Aleaked-after-newline", "PASS\nWORD=S3CR28", "TOKEN=\nS3CR29", "token%3dS3CR30",
	} {
		got := redactPluginDiagnostic(line)
		if strings.Contains(got, "S3CR") || strings.Contains(got, "leaked-after-newline") || !strings.Contains(got, "[redacted]") {
			t.Errorf("secret leaked: %q => %q", line, got)
		}
	}
}

func TestLoadFailurePreservesFollowingStderrCause(t *testing.T) {
	host, _ := newHost(t)
	spec := echoSpec(t, buildEchoPlugin(t))
	spec.Env = append(spec.Env, "ECHO_PLUGIN_INIT_ERROR=1", "ECHO_PLUGIN_STDERR=TOKEN=S3CR01\nIMPORTANT CAUSE: database migration 42 failed\nFINAL CAUSE: schema migration failed")
	child := NewChildPlugin(spec, nil, nil)
	t.Cleanup(func() { _ = child.Unload() })
	err := host.Load(child)
	if err == nil {
		t.Fatal("loaded invalid fixture")
	}
	record := host.Inventory(context.Background()).Plugins[0]
	for _, text := range []string{err.Error(), record.Error} {
		if strings.Contains(text, "S3CR01") || !strings.Contains(text, "FINAL CAUSE: schema migration failed") || !strings.Contains(text, "IMPORTANT CAUSE") {
			t.Fatal(text)
		}
	}
}

func TestDiagnosticStderrCorpusRetainsFinalCause(t *testing.T) {
	raw, err := os.ReadFile("testdata/diagnostic-stderr.txt")
	if err != nil {
		t.Fatal(err)
	}
	text := redactRawDiagnostic(string(raw))
	text = redactPluginDiagnostic("pluginhost: load/init: plugin is gone\nplugin stderr:\n" + text)
	if strings.Contains(text, "S3CR") || strings.Contains(text, "UzNDUjA2") || !strings.Contains(text, "FINAL CAUSE: schema migration failed") || !strings.Contains(text, "level=ERROR msg=forged") {
		t.Fatal(text)
	}
}

func TestRawDiagnosticDefersDisplaySanitization(t *testing.T) {
	raw := "TOKEN=hidden\nCAUSE:\tconnection failed\x1b[0m\n"
	got := redactRawDiagnostic(raw)
	if got != "TOKEN=[redacted]\nCAUSE:\tconnection failed\x1b[0m\n" {
		t.Fatalf("raw text sanitized before wrapping: %q", got)
	}
	if display := sanitizePluginDiagnostic(got); display != "TOKEN=[redacted]?CAUSE:?connection failed?[0m?" {
		t.Fatalf("unsafe display text: %q", display)
	}
}
