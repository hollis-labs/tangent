package pluginhost

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"

	driver "github.com/hollis-labs/plugin-host"
)

func TestDiagnosticRedactionExpandedFormats(t *testing.T) {
	cases := []string{
		"KEY=SECRET0001", "PRIVATE_KEY=SECRET0002", "passwd=SECRET0003", "auth=SECRET0004", "credentials=SECRET0005",
		"Authorization: Basic U0VDUkVUMDAwNg==", "Authorization: Token SECRET0007", "postgres://u:SECRET0008@db", "mysql://SECRET0009@db",
		`msg="{\"token\":\"SECRET0010 with spaces,commas\"}"`, "token%3DSECRET0011", "--token SECRET0012", "MYTOKEN=SECRET0013",
		"xoxb-SECRET0014-abc", "AKIASECRET0015ABCDEFG", "token\u200b=SECRET0016", "ｔｏｋｅｎ=SECRET0017", "TOKEN=SEC\x1bRET0018",
		`{"token":"SECRET0019 with spaces,commas"}`, "password=SECRET0020 with spaces,commas", "client_secret: SECRET0021", "x-api-key: SECRET0022",
	}
	for _, line := range cases {
		got := redactPluginDiagnostic(line)
		if !strings.Contains(got, "[redacted]") || strings.Contains(got, "SECRET") || strings.Contains(got, "U0VDUkVU") || strings.Contains(got, "RET0018") || strings.Contains(got, "spaces") {
			t.Errorf("redaction %q => %q", line, got)
		}
	}
}

func TestDiagnosticPreservesProseAndSanitizesControls(t *testing.T) {
	for _, line := range []string{"invalid token: expired", "password: authentication failed", "open token: no such file", "api_key: missing", "secret=; using default"} {
		if got := redactPluginDiagnostic(line); got != line {
			t.Errorf("cause hidden: %q => %q", line, got)
		}
	}
	got := redactPluginDiagnostic("error\x1b[31m\r\n forged\u202emessage")
	for _, r := range got {
		if !unicode.IsPrint(r) || unicode.Is(unicode.Cf, r) {
			t.Fatalf("unsafe character remains: %q", got)
		}
	}
}

func TestDiagnosticLongErrorKeepsLabelAndCause(t *testing.T) {
	text := "pluginhost: load/init (init_failed): plugin is gone\n" + strings.Repeat("filler line\n", 500) + "FINAL CAUSE: schema failed"
	got := redactPluginDiagnostic(text)
	if !strings.HasPrefix(got, "pluginhost: load/init (init_failed): plugin is gone") || !strings.Contains(got, "FINAL CAUSE: schema failed") {
		t.Fatal("generic error trimmed", got)
	}
}

func TestLoadFailureStderrIsScrubbed(t *testing.T) {
	host, _ := newHost(t)
	spec := echoSpec(t, buildEchoPlugin(t))
	spec.Env = append(spec.Env, "ECHO_PLUGIN_INIT_ERROR=1", "ECHO_PLUGIN_STDERR=KEY=SECRET-value")
	child := NewChildPlugin(spec, nil, nil)
	t.Cleanup(func() { _ = child.Unload() })
	err := host.Load(child)
	if err == nil {
		t.Fatal("fixture unexpectedly loaded")
	}
	record := host.Inventory(context.Background()).Plugins[0]
	if strings.Contains(err.Error(), "SECRET-value") || strings.Contains(record.Error, "SECRET-value") || !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("load diagnostic: %v %+v", err, record)
	}
}

func TestFailedRestartIsVisibleAndLoggedOnce(t *testing.T) {
	host, _ := newHost(t)
	var logs lockedLogBuffer
	host.logger = slog.New(slog.NewTextHandler(&logs, nil))
	binary := buildEchoPlugin(t)
	child := NewChildPlugin(echoSpec(t, binary), nil, nil)
	t.Cleanup(func() { _ = child.Unload() })
	if err := host.Load(child); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	if err := killCurrent(child); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for child.lifecycle.Status().State != driver.StateFailed || child.Restarts() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("failed restart did not terminate")
		}
		time.Sleep(10 * time.Millisecond)
	}
	record := host.Inventory(context.Background()).Plugins[0]
	if !record.Loaded || !record.FailedAfterLoad || record.Error != "" || !strings.Contains(record.RuntimeError, "no such file or directory") {
		t.Fatalf("failed restart invisible or called refused: %+v", record)
	}
	_ = host.Inventory(context.Background())
	_ = child.Status()
	if strings.Count(logs.String(), "plugin failed after load") != 1 || !strings.Contains(logs.String(), "no such file or directory") {
		t.Fatalf("restart diagnostic: %s", logs.String())
	}
}

func TestRelativePluginPathsResolveBeforeSpawn(t *testing.T) {
	host, _ := newHost(t)
	binary := buildEchoPlugin(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	spec := echoSpec(t, binary)
	spec.Command, err = filepath.Rel(cwd, binary)
	if err != nil {
		t.Fatal(err)
	}
	spec.WorkDir, err = filepath.Rel(cwd, filepath.Dir(binary))
	if err != nil {
		t.Fatal(err)
	}
	child := NewChildPlugin(spec, nil, nil)
	t.Cleanup(func() { _ = child.Unload() })
	if err := host.Load(child); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(child.spec.Command) || !filepath.IsAbs(child.spec.WorkDir) {
		t.Fatal("relative spawn paths retained")
	}
}
