package replymail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// TetherSender posts reply mail to Tether's notify endpoint:
//
//	POST {URL}/messages/notify  Authorization: Bearer <token>  Idempotency-Key: <key>
//	{"from": sender, "to": reply_to, "kind": "response", "payload": {...}}
//
// The credential is a send-only service token issued for Tangent. Tether stores
// the mail either way and wakes the recipient when it can, so a not-woken
// answer is still a delivery.
type TetherSender struct {
	URL    string
	Token  string
	Sender string
	Client *http.Client
}

// Send implements Sender.
func (t *TetherSender) Send(ctx context.Context, msg Message) (Receipt, error) {
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	body, err := json.Marshal(map[string]any{
		"from": t.Sender, "to": msg.To, "kind": msg.Kind, "payload": msg.Payload,
	})
	if err != nil {
		return Receipt{}, &SendError{Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(t.URL, "/")+"/messages/notify", bytes.NewReader(body))
	if err != nil {
		return Receipt{}, &SendError{Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.Token)
	if msg.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", msg.IdempotencyKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		// Never wrap the request: it carries the bearer token.
		return Receipt{}, &SendError{Retryable: true, Err: errors.New(redactURLError(err))}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return parseReceipt(raw), nil
	}
	retryable := resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests
	return Receipt{}, &SendError{Retryable: retryable, Status: resp.StatusCode, Err: fmt.Errorf("%s", truncate(string(raw), 300))}
}

// parseReceipt reads what Tether's notify reply says, tolerantly: the message
// id and the wake outcome (delivered true/false with a reason).
func parseReceipt(raw []byte) Receipt {
	receipt := Receipt{}
	if len(raw) <= 4096 && json.Valid(raw) {
		receipt.Raw = json.RawMessage(raw)
	}
	var top map[string]any
	if json.Unmarshal(raw, &top) != nil {
		return receipt
	}
	msg, _ := top["message"].(map[string]any)
	if msg == nil {
		msg = top
	}
	if id, ok := msg["id"].(string); ok {
		receipt.MessageID = id
	}
	wake, _ := top["wake"].(map[string]any)
	if wake == nil {
		wake, _ = top["wake_outcome"].(map[string]any)
	}
	if wake == nil {
		if meta, _ := msg["metadata"].(map[string]any); meta != nil {
			if s, ok := meta["tether.wake_outcome"].(string); ok {
				_ = json.Unmarshal([]byte(s), &wake)
			}
		}
	}
	if wake != nil {
		receipt.Woken, _ = wake["delivered"].(bool)
		receipt.Reason, _ = wake["reason"].(string)
	}
	return receipt
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func redactURLError(err error) string {
	var ue interface{ Unwrap() error }
	if errors.As(err, &ue) && ue.Unwrap() != nil {
		return ue.Unwrap().Error()
	}
	return "network error"
}
