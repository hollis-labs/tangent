package turns

import (
	"context"
	"errors"
	"testing"
)

func TestReplyInterruptIsDurableImmutableAndAbsentMeansFalse(t *testing.T) {
	for _, flag := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy_false", true: "explicit_true"}[flag], func(t *testing.T) {
			svc, _, _ := newTestService(t)
			ctx := context.Background()
			h, err := svc.Enqueue(ctx, EnqueueInput{Request: turnRequestJSON("session-interrupt", "turn", "question", "Choose", "Original"), Caller: testCaller("synthetic")})
			if err != nil {
				t.Fatal(err)
			}
			view, err := svc.Reply(ctx, ReplyInput{ItemID: h.ItemID, ExpectedRevision: h.Revision, Action: "respond", ResponseText: "Exact reply", Interrupt: flag})
			if err != nil {
				t.Fatal(err)
			}
			if view.Resolution == nil || view.Resolution.Interrupt != flag {
				t.Fatalf("resolution %+v", view.Resolution)
			}
			replies, err := svc.SessionReplies(ctx, "session-interrupt")
			if err != nil || len(replies) != 1 || replies[0].Resolution.Interrupt != flag {
				t.Fatalf("await %+v %v", replies, err)
			}
			_, err = svc.Reply(ctx, ReplyInput{ItemID: h.ItemID, ExpectedRevision: view.Revision, Action: "respond", ResponseText: "Changed", Interrupt: !flag})
			if !errors.Is(err, ErrTerminalConflict) {
				t.Fatalf("immutable resolution changed: %v", err)
			}
			again, err := svc.InspectTurn(ctx, h.ItemID)
			if err != nil || again.Resolution.ResolutionID != view.Resolution.ResolutionID || again.Resolution.Interrupt != flag || again.Resolution.ResponseText != "Exact reply" {
				t.Fatal("stored user intent changed")
			}
		})
	}
}
