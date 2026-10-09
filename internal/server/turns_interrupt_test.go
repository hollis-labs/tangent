package server

import (
	"context"
	"github.com/hollis-labs/tangent/internal/turns"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type interruptService struct {
	TurnsService
	calls int
	input turns.ReplyInput
}

func (s *interruptService) Reply(_ context.Context, in turns.ReplyInput) (turns.TurnItemView, error) {
	s.calls++
	s.input = in
	return turns.TurnItemView{}, nil
}
func TestTurnsReplyInterruptStrictOptionalBoolean(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		valid, want bool
	}{
		{"absent", "", true, false}, {"true", `,"interrupt":true`, true, true}, {"false", `,"interrupt":false`, true, false},
		{"null", `,"interrupt":null`, false, false}, {"string", `,"interrupt":"true"`, false, false}, {"number", `,"interrupt":1`, false, false}, {"object", `,"interrupt":{}`, false, false},
		{"duplicate", `,"interrupt":true,"interrupt":false`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &interruptService{}
			h := newTurnsHTTPHandler(s)
			r := httptest.NewRequest(http.MethodPost, "/api/turns/items/item/reply", strings.NewReader(`{"expected_revision":7,"action":"respond","response_text":"exact body"`+tc.value+`}`))
			r.Header.Set("Content-Type", "application/json")
			r.SetPathValue("itemID", "item")
			w := httptest.NewRecorder()
			h.reply(w, r)
			if tc.valid {
				if w.Code != 200 || s.calls != 1 || s.input.Interrupt != tc.want || s.input.ExpectedRevision != 7 || s.input.ResponseText != "exact body" {
					t.Fatalf("mapping %d %+v", w.Code, s.input)
				}
			} else if w.Code != 400 || s.calls != 0 {
				t.Fatalf("invalid dispatched %d calls=%d", w.Code, s.calls)
			}
		})
	}
}
