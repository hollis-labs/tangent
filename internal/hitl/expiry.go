package hitl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/tangent/internal/interaction"
)

const expiryPolicyRef = "request.expires_at"

// ExpiryActor is the in-process host-policy actor that materializes
// request.expires_at. Only the HITL service asserts it; direct MCP callers
// never hold it.
var ExpiryActor = interaction.ActorBinding{
	Scope:        "host:hitl-expiry",
	PrincipalRef: "hitl-expiry",
	Authority:    "tangent-host-policy",
	Assurance:    "asserted",
}

// ExpiryPolicy authorizes ExpiryActor for host-policy operations and defers
// every other question (and administrator authority) to Next.
type ExpiryPolicy struct {
	Next interaction.PrivilegedActorPolicy
}

func (p ExpiryPolicy) AuthorizeHostPolicy(actor interaction.ActorBinding) bool {
	return actor == ExpiryActor || (p.Next != nil && p.Next.AuthorizeHostPolicy(actor))
}

func (p ExpiryPolicy) AuthorizeAdministrator(actor interaction.ActorBinding) bool {
	return p.Next != nil && p.Next.AuthorizeAdministrator(actor)
}

// itemExpiry returns the item's expires_at from its recorded policy.
func itemExpiry(record interaction.InteractionRecord) (time.Time, bool) {
	var policy struct {
		ExpiresAt string `json:"expires_at"`
	}
	if json.Unmarshal(record.Policy, &policy) != nil || policy.ExpiresAt == "" {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, policy.ExpiresAt)
	return at, err == nil
}

// maxExpiryAttempts bounds the re-read loop in expireIfDue.
const maxExpiryAttempts = 5

// expireIfDue compare-and-sets a nonterminal item whose expires_at has been
// reached (now >= expires_at) to expired. It reports whether the item is
// terminal afterwards. When another writer wins the compare-and-set with a
// non-terminal write (for example a presentation ack), it re-reads and retries,
// so a due item is never reported as live.
func (s *Service) expireIfDue(ctx context.Context, record interaction.InteractionRecord) (bool, error) {
	for attempt := 0; attempt < maxExpiryAttempts; attempt++ {
		if isTerminal(record.State) {
			return attempt > 0, nil
		}
		at, ok := itemExpiry(record)
		if !ok || s.now().Before(at) {
			return false, nil
		}
		_, err := s.interactions.ExpireInteraction(ctx, interaction.ExpireInteractionInput{
			InteractionID: record.ID, ExpectedRevision: record.Revision,
			PolicyRef: expiryPolicyRef, Actor: ExpiryActor, Capability: reservedSurfaceCapability,
		})
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, interaction.ErrRevisionConflict) && !errors.Is(err, interaction.ErrTerminal) {
			return false, err
		}
		latest, readErr := s.interactions.InspectInteraction(ctx, interaction.GetInteractionInput{
			InteractionID: record.ID, RequesterScope: OperatorParticipant.Scope, Capability: reservedSurfaceCapability,
		})
		if readErr != nil {
			return false, readErr
		}
		record = latest.Interaction
	}
	return false, fmt.Errorf("hitl: item %q kept changing while expiring", record.ID)
}

// ExpireDue expires every pending HITL item whose deadline has passed and
// returns how many this call expired.
func (s *Service) ExpireDue(ctx context.Context) (int, error) {
	snapshot, err := s.interactions.GetSurfaceInteractions(ctx, interaction.GetSurfaceInput{
		SurfaceID: DefaultSurfaceID, RequesterScope: OperatorParticipant.Scope, Capability: reservedSurfaceCapability,
	})
	if errors.Is(err, interaction.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	expired := 0
	var errs []error
	for _, record := range snapshot.Interactions {
		if !isHITLRecord(record) {
			continue
		}
		done, expireErr := s.expireIfDue(ctx, record)
		if expireErr != nil {
			// One failing item must not stop the rest from expiring.
			errs = append(errs, expireErr)
			continue
		}
		if done {
			expired++
		}
	}
	return expired, errors.Join(errs...)
}

// RunExpirySweeper calls ExpireDue every interval until ctx is done. Errors are
// passed to onError and never stop the loop.
func (s *Service) RunExpirySweeper(ctx context.Context, interval time.Duration, onError func(error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.ExpireDue(ctx); err != nil && ctx.Err() == nil && onError != nil {
				onError(err)
			}
		}
	}
}
