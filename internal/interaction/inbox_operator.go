package interaction

import (
	"context"
	"fmt"
)

func isActiveState(state InteractionState) bool {
	switch state {
	case InteractionStateResolved, InteractionStateCanceled, InteractionStateExpired,
		InteractionStateFailed, InteractionStateSuperseded:
		return false
	}
	return true
}

// ArchiveBrowserInboxItem lets the operator close any Inbox item that is still
// open, whatever its kind: the item ends as canceled and moves to History, and
// the asking agent hears it ended (caller-pull notification, plus reply mail
// when it gave a reply_to). An item that is already closed is left as it is.
//
// Tangent holds this data only to put it in front of the operator, so ending an
// item from the browser is the operator's call; it needs no administrator
// policy beyond the Inbox's own authority-wide check.
func (s *Service) ArchiveBrowserInboxItem(ctx context.Context, id string) error {
	record, err := s.store.GetInteraction(ctx, id)
	if err != nil {
		return err
	}
	if err = s.authorizeOperatorInbox(record); err != nil {
		return err
	}
	if !isActiveState(record.State) {
		return nil
	}
	_, err = s.store.TerminalizeInteraction(ctx, TerminalizeInteractionParams{
		InteractionID: id, ExpectedRevision: record.Revision,
		To: InteractionStateCanceled, Cause: TerminalCauseAdministratorCanceled,
		Reason:   "archived by the operator from the Inbox",
		ActorRef: "local-operator", Authority: "operator",
		Notifications: withTetherMailNotification(record, "cancel",
			[]TerminalNotificationParams{callerPullTerminalNotification(record, "cancel")}),
	})
	return err
}

// DeleteBrowserInboxItem removes an item from the Inbox. An open item is
// archived first, so the asking agent is told it ended; the record itself stays
// for the agent's delivery and recovery, like every other kind's delete.
func (s *Service) DeleteBrowserInboxItem(ctx context.Context, id string) error {
	if err := s.ArchiveBrowserInboxItem(ctx, id); err != nil {
		return fmt.Errorf("archive before delete: %w", err)
	}
	return s.HideBrowserInboxItem(ctx, id)
}
