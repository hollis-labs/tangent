package main

import (
	"context"
	"strings"
	"testing"

	libsecrets "github.com/hollis-labs/libs/security/secrets"
)

func TestReplyTokenPrefersTheReferenceAndFailsLoudly(t *testing.T) {
	r := libsecrets.New(libsecrets.WithStateDir(t.TempDir()))
	env := func(k string) string {
		if k == envTetherSendOnly {
			return "from-env"
		}
		return ""
	}
	ctx := context.Background()
	if got, err := replyToken(ctx, r, "", env); err != nil || got != "from-env" {
		t.Fatalf("no reference: got %q, %v", got, err)
	}
	const ref = "file://tangent/tether-send-only"
	if err := r.Set(ctx, ref, strings.NewReader("from-store")); err != nil {
		t.Fatal(err)
	}
	if got, err := replyToken(ctx, r, ref, env); err != nil || got != "from-store" {
		t.Fatalf("reference: got %q, %v", got, err)
	}
	if _, err := replyToken(ctx, r, "file://tangent/absent", env); err == nil {
		t.Fatal("an unresolvable reference must stop the start, not fall back to the environment")
	}
}
