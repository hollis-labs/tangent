package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"

	driver "github.com/hollis-labs/plugin-host"
)

func TestRound4VendorAndSegmentCredentialNames(t *testing.T) {
	for _, name := range []string{"OPENAI_API_KEY", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "DATABASE_PASSWORD", "POSTGRES_PASSWORD", "GITHUB_TOKEN", "STRIPE_SECRET_KEY", "TWILIO_AUTH_TOKEN", "JWT_SECRET", "SECRET_KEY_BASE", "auth_token", "x_api_key", "my_api_key", "MY_SERVICE_TOKEN", "apiKey", "accessToken", "clientSecret", "apikey", "pass", "passphrase", "api-key", "access-token", "client-secret", "secret_key", "access_token", "access_key", "db_password", "db_key", "client_key", "session_key", "refresh_key", "signing_key", "encryption_key", "private_key", "tokens", "secrets", "credential", "credentials"} {
		for _, line := range []string{name + "=hidden-value", `{"` + name + `":"hidden-value"}`, "--" + name + " hidden-value"} {
			if got := redactPluginDiagnostic(line); strings.Contains(got, "hidden-value") || !strings.Contains(got, "[redacted]") {
				t.Errorf("name %s: %q", name, got)
			}
		}
	}
}

func TestRound4AssignmentKeepsTrailingDiagnosticFields(t *testing.T) {
	cases := []struct{ raw, want string }{
		{`Get "https://api/v1?api_key=hidden": dial tcp: lookup host`, `Get "https://api/v1?api_key=[redacted]": dial tcp: lookup host`},
		{`token=hidden err="connection refused"`, `token=[redacted] err="connection refused"`},
		{`client_secret=hidden&grant_type=client_credentials`, `client_secret=[redacted]&grant_type=client_credentials`},
		{`{"msg":"auth failed token=hidden","err":"connection refused"}`, `{"msg":"auth failed token=[redacted]","err":"connection refused"}`},
		{"API_KEY=\nCAUSE: original failure", "API_KEY=?CAUSE: original failure"},
		{"cache key=users:42 miss; cause: boom", "cache key=[redacted] miss; cause: boom"},
		{"auth=basic unsupported mode", "auth=[redacted] unsupported mode"},
		{"PWD=/home/u/plugins", "PWD=/home/u/plugins"},
		{"pwd=/home/u/plugins", "pwd=/home/u/plugins"},
		{"--auth-mode basic failed: unsupported", "--auth-mode basic failed: unsupported"},
		{"token%X", "token%X"},
		{"token%", "token%"},
	}
	for _, tc := range cases {
		if got := redactPluginDiagnostic(tc.raw); got != tc.want {
			t.Errorf("%q => %q want %q", tc.raw, got, tc.want)
		}
	}
}

func TestRound4CheapCredentialPatterns(t *testing.T) {
	for _, raw := range []string{
		"Authorization: Digest response=hidden-value", "Authorization: ApiKey hidden-value", "Authorization: hidden-value",
		"custom://hidden-value@host", "redis://u:hidden-value@host", "mongodb://hidden-value@host", "app:hidden-value@tcp(host)/db",
		"-----BEGIN RSA PRIVATE KEY-----\nhidden-value\n-----END RSA PRIVATE KEY-----",
		"-----BEGIN PRIVATE KEY-----\nhidden-value\n-----END PRIVATE KEY-----",
		"eyJabc.def.hidden-value", "ASIAHIDDENVALUE0000", "glpat-hidden-value", "AIzaHiddenValue0000", "npm_hidden-value", "SG.hidden-value.hidden-value",
		"ghp_hiddenvalue0000", "token\\u003dhidden-value", "tok\\u200ben=hidden-value", "token\u200b=hidden-value",
	} {
		got := redactPluginDiagnostic(raw)
		if strings.Contains(got, "hidden-value") || strings.Contains(got, "HIDDENVALUE") || strings.Contains(got, "HiddenValue") || strings.Contains(got, "hiddenvalue") || !strings.Contains(got, "[redacted]") {
			t.Errorf("%q => %q", raw, got)
		}
	}
}

func TestRound4ReviewerCorpusAndExplicitLimitations(t *testing.T) {
	var raw strings.Builder
	var first []byte
	for _, name := range []string{"A", "B", "C", "D"} {
		data, err := os.ReadFile("testdata/round4/" + name + ".txt") //nolint:gosec // fixed test-owned corpus names, no external input
		if err != nil {
			t.Fatal(err)
		}
		if name == "A" {
			first = data
		}
		raw.Write(data)
	}
	data, err := os.ReadFile("testdata/round4/secrets.json")
	if err != nil {
		t.Fatal(err)
	}
	var secrets map[string][]string
	if err := json.Unmarshal(data, &secrets); err != nil {
		t.Fatal(err)
	}
	for _, secret := range regexp.MustCompile(`S3CR[0-9][0-9]`).FindAllString(string(first), -1) {
		secrets[secret] = nil
	}
	secrets["UzNDUjA2"] = nil
	// These observations are intentionally outside the approved name and value rules.
	// Each is asserted as visible, so the report cannot imply universal secrecy.
	outside := map[string]string{"S4CR21": "-p flag", "S4CR30": "YAML/bare colon", "S5CR13": "arbitrary wrapped token"}
	got := redactPluginDiagnostic(raw.String())
	for secret := range secrets {
		if why, ok := outside[secret]; ok {
			if !strings.Contains(got, secret) {
				t.Errorf("classified limitation %s (%s) unexpectedly hidden", secret, why)
			}
			continue
		}
		if strings.Contains(got, secret) {
			t.Errorf("corpus credential leaked %s: %v", secret, secrets[secret])
		}
	}
	for _, cause := range []string{"IMPORTANT CAUSE: database migration 42 failed", "FINAL CAUSE: schema migration failed", "connection refused", "KEEP01", "KEEP11", "100%25", "a%2Fb"} {
		if !strings.Contains(got, cause) {
			t.Errorf("corpus diagnostic lost %q", cause)
		}
	}
}

func TestRound4PinnedTailCutsPartialKeyBeforeLogging(t *testing.T) {
	host, _ := newHost(t)
	spec := echoSpec(t, buildEchoPlugin(t))
	// 505 bytes survive before the truncated PASSWORD line's newline.
	raw := strings.Repeat("p", 501) + "PASSWORD=S4CRCUT\nFINAL CAUSE: failure\n"
	raw += strings.Repeat("p", 4600-len(raw))
	spec.Env = append(spec.Env, "ECHO_PLUGIN_INIT_ERROR=1", "ECHO_PLUGIN_STDERR="+raw)
	child := NewChildPlugin(spec, nil, nil)
	t.Cleanup(func() { _ = child.Unload() })
	err := host.Load(child)
	if err == nil {
		t.Fatal("invalid child loaded")
	}
	for _, text := range []string{err.Error(), host.Inventory(context.Background()).Plugins[0].Error} {
		if strings.Contains(text, "S4CRCUT") || strings.Contains(text, "WORD=") || !strings.Contains(text, "FINAL CAUSE") {
			t.Fatal(text)
		}
	}
}

// Isolate terminal-failure display formatting with a real lifecycle failure.
// Real-child crash/exhaustion behavior remains covered by restart_test.go.
func TestRound4RuntimeFailureIsRedactedSanitizedAndBounded(t *testing.T) {
	for _, long := range []bool{false, true} {
		diagnostic := "OPENAI_API_KEY=hidden-value\nCAUSE:\x1b[0m failure"
		if long {
			diagnostic += "\n" + strings.Repeat("é", 3000)
		}
		controller, err := driver.NewLifecycle("tangent.plugin.echo", driver.LifecycleOptions{
			HostInstance: "test-host", Generations: &driver.MemoryGenerationStore{},
			Callbacks: driver.LifecycleCallbacks{Plan: func(context.Context) (driver.Plan, error) { return driver.Plan{}, errors.New(diagnostic) }},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := controller.Enable(context.Background()); err == nil {
			t.Fatal("invalid plan enabled")
		}
		child := NewChildPlugin(ChildSpec{ID: "tangent.plugin.echo"}, nil, nil)
		child.lifecycle = controller
		child.status.LoadedAt = time.Now()
		got := child.RuntimeFailure()
		if strings.Contains(got, "hidden-value") || !strings.Contains(got, "[redacted]") || !strings.Contains(got, "CAUSE:") || len(got) > 2048 {
			t.Fatalf("runtime diagnostic: %q", got)
		}
		for _, r := range got {
			if !unicode.IsPrint(r) || unicode.Is(unicode.Cf, r) {
				t.Fatalf("unsafe runtime display %q", got)
			}
		}
	}
}

func TestRound4LoadErrorDisplaySanitizesControls(t *testing.T) {
	host, _ := newHost(t)
	spec := echoSpec(t, buildEchoPlugin(t))
	spec.Env = append(spec.Env, "ECHO_PLUGIN_INIT_ERROR=1", "ECHO_PLUGIN_STDERR=CAUSE:\x1b[31m migration failed\nOPENAI_API_KEY=hidden-value")
	child := NewChildPlugin(spec, nil, nil)
	t.Cleanup(func() { _ = child.Unload() })
	err := host.Load(child)
	if err == nil {
		t.Fatal("invalid child loaded")
	}
	var failure *driver.Failure
	if !errors.As(err, &failure) {
		t.Fatal("typed failure lost", err)
	}
	for _, text := range []string{err.Error(), host.Inventory(context.Background()).Plugins[0].Error} {
		if strings.Contains(text, "hidden-value") || !strings.Contains(text, "migration failed") {
			t.Fatal(text)
		}
		for _, r := range text {
			if !unicode.IsPrint(r) || unicode.Is(unicode.Cf, r) {
				t.Fatalf("unsafe load display %q", text)
			}
		}
	}
}
