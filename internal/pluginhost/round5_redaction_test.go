package pluginhost

import "testing"

func TestRound5BareCredentialValuesAndAbsolutePWD(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"KEY=S3CR01 cause: boom", "KEY=[redacted] cause: boom"},
		{"key=S3CR01 err=boom", "key=[redacted] err=boom"},
		{"auth=S3CR04 cause: boom", "auth=[redacted] cause: boom"},
		{"AUTH=S3CR04&keep=yes", "AUTH=[redacted]&keep=yes"},
		{"pwd=S5CR09 cause: boom", "pwd=[redacted] cause: boom"},
		{"PWD=S5CR09", "PWD=[redacted]"},
		{"pWd=S5CR09", "pWd=[redacted]"},
		{"pass=hidden cause: boom", "pass=[redacted] cause: boom"},
		{"cache key=users:42 miss; cause: boom", "cache key=[redacted] miss; cause: boom"},
		{"auth=none cause: x", "auth=[redacted] cause: x"},
		{"api_\nkey=S5CR12\nFINAL CAUSE: failure", "api_\nkey=[redacted]\nFINAL CAUSE: failure"},
		{"KEY=\nNEXT LINE cause: boom", "KEY=\nNEXT LINE cause: boom"},
		{"PWD=/home/u/plugins cause: boom", "PWD=/home/u/plugins cause: boom"},
		{`pwd="/home/u/plugins" err=boom`, `pwd="/home/u/plugins" err=boom`},
		{`{"PWD":"/home/u/plugins","err":"boom"}`, `{"PWD":"/home/u/plugins","err":"boom"}`},
		{`{"auth":"hidden value","err":"boom"}`, `{"auth":[redacted],"err":"boom"}`},
		{"project_key=TQ monkey=1 keyspace=ks", "project_key=TQ monkey=1 keyspace=ks"},
	}
	for _, tc := range cases {
		if got := redactRawDiagnostic(tc.raw); got != tc.want {
			t.Errorf("%q => %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestRound5QuotedURLIsNotACredentialName(t *testing.T) {
	for _, value := range []string{"hunterSECRET", "abcSECRET", "NoTokenHere", "q7x2m9k4", "Zk9Pq"} {
		raw := `Get "https://api/v1?api_key=` + value + `": dial tcp: lookup db: no such host`
		want := `Get "https://api/v1?api_key=[redacted]": dial tcp: lookup db: no such host`
		if got := redactRawDiagnostic(raw); got != want {
			t.Errorf("%q => %q, want %q", raw, got, want)
		}
	}
	for _, raw := range []string{`open "/etc/app.conf": permission denied`, `msg "something": cause: boom`} {
		if got := redactRawDiagnostic(raw); got != raw {
			t.Errorf("ordinary quoted diagnostic lost: %q", got)
		}
	}
}
