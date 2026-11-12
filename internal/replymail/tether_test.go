package replymail

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTetherSenderPostsNotifyWithIdempotencyKeyAndReadsTheWakeOutcome(t *testing.T) {
	var got struct {
		path, auth, idem string
		body             map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.auth, got.idem = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key")
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"message":{"id":"m1","metadata":{"tether.wake_outcome":"{\"attempted\":false,\"delivered\":false,\"reason\":\"offline\"}"}}}`))
	}))
	defer srv.Close()

	sender := &TetherSender{URL: srv.URL + "/", Token: "tok-secret", Sender: "msg://service/tangent/dev"}
	receipt, err := sender.Send(context.Background(), Message{
		To: "msg://agent/agent-mux/agt_x", Kind: "response", IdempotencyKey: "tangent:resolution:i1:3",
		Payload: map[string]any{"subject": "s", "body": "b"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.path != "/messages/notify" || got.auth != "Bearer tok-secret" || got.idem != "tangent:resolution:i1:3" {
		t.Fatalf("request = %+v", got)
	}
	if got.body["from"] != "msg://service/tangent/dev" || got.body["to"] != "msg://agent/agent-mux/agt_x" || got.body["kind"] != "response" {
		t.Fatalf("body = %v", got.body)
	}
	if receipt.MessageID != "m1" || receipt.Woken || receipt.Reason != "offline" {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestTetherSenderClassifiesFailuresAndNeverLeaksTheToken(t *testing.T) {
	status := http.StatusInternalServerError
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	}))
	defer srv.Close()
	sender := &TetherSender{URL: srv.URL, Token: "tok-secret", Sender: "s"}
	for code, wantRetry := range map[int]bool{500: true, 503: true, 429: true, 401: false, 403: false, 400: false} {
		status = code
		_, err := sender.Send(context.Background(), Message{To: "msg://a/b", Kind: "response"})
		var se *SendError
		if !errors.As(err, &se) || se.Retryable != wantRetry || se.Status != code {
			t.Fatalf("status %d: err = %v, want retryable=%v", code, err, wantRetry)
		}
	}
	srv.Close()
	_, err := sender.Send(context.Background(), Message{To: "msg://a/b", Kind: "response"})
	var se *SendError
	if !errors.As(err, &se) || !se.Retryable {
		t.Fatalf("a network failure must be retryable, got %v", err)
	}
	if strings.Contains(err.Error(), "tok-secret") {
		t.Fatalf("the token leaked into the error: %v", err)
	}
}

func TestBackoffDoublesAndCaps(t *testing.T) {
	if backoff(1) != 30*time.Second || backoff(2) != time.Minute || backoff(3) != 2*time.Minute {
		t.Fatalf("backoff = %v %v %v", backoff(1), backoff(2), backoff(3))
	}
	if backoff(40) != 4*time.Hour {
		t.Fatalf("backoff(40) = %v", backoff(40))
	}
}
