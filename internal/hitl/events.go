package hitl

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/hollis-labs/libs/harness/core/adapters/runtimeevents/catalog"

	"github.com/hollis-labs/tangent/internal/events"
	"github.com/hollis-labs/tangent/internal/interaction"
)

// EventObserver turns approval-kind HITL transitions into approval.requested
// and approval.resolved outbox rows. It runs inside the transition's own
// transaction, so the event row commits with the journal row or not at all.
// The events are notifications only: nothing may gate on them, because
// approvals are not a security boundary yet (CW-20261016-0174).
type EventObserver struct {
	Emitter events.Emitter
}

var _ interaction.TransitionObserver = EventObserver{}

// ObserveTransition implements interaction.TransitionObserver.
func (o EventObserver) ObserveTransition(ctx context.Context, tx *sql.Tx, t interaction.Transition) error {
	record := t.Interaction
	if !isHITLRecord(record) || requestKind(record.RequestSnapshot) != "approval" {
		return nil
	}
	approval := events.Approval{ItemID: record.ID, Class: "approval", At: t.At, Requester: callerPrincipal(record)}
	switch t.Phase {
	case interaction.PhaseCreated:
		approval.Kind = catalog.KindApprovalRequested
		approval.ExpiresAt = policyExpiry(record.Policy)
	case interaction.PhaseResolved:
		approval.Kind = catalog.KindApprovalResolved
		approval.Outcome = decisionOutcome(t.ResponsePayload)
		approval.Resolver = actorPrincipal(t.ActorRef, t.Authority, "operator")
		approval.Resolver.Assurance = t.Assurance
	case interaction.PhaseTerminal:
		approval.Kind = catalog.KindApprovalResolved
		approval.Outcome = terminalOutcomeName(t.To)
		approval.Resolver = terminalResolver(t, approval.Requester)
		if t.To == interaction.InteractionStateFailed {
			// failed is not in the approval.resolved vocabulary: close it as
			// withdrawn by the service, and only if the request was announced.
			approval.Resolver = events.ServicePrincipal
			announced, err := events.HasRequested(ctx, tx, record.ID)
			if err != nil || !announced {
				return err
			}
		}
	default:
		return nil
	}
	_, err := o.Emitter.Emit(ctx, tx, approval)
	return err
}

func requestKind(snapshot json.RawMessage) string {
	var r struct {
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(snapshot, &r)
	return r.Kind
}

func policyExpiry(policy json.RawMessage) string {
	var p struct {
		ExpiresAt string `json:"expires_at"`
	}
	_ = json.Unmarshal(policy, &p)
	return p.ExpiresAt
}

// decisionOutcome reads only the decision verb from the response; the note and
// every other response field stay out of the event.
func decisionOutcome(response json.RawMessage) string {
	var r struct {
		Decision string `json:"decision"`
	}
	_ = json.Unmarshal(response, &r)
	switch r.Decision {
	case "approved":
		return events.OutcomeApproved
	case "denied":
		return events.OutcomeDenied
	}
	return "" // fails the sanitizer's closed vocabulary rather than guessing
}

func terminalOutcomeName(to interaction.InteractionState) string {
	switch to {
	case interaction.InteractionStateExpired:
		return events.OutcomeExpired
	default: // canceled, superseded: the request was closed without a decision
		return events.OutcomeWithdrawn
	}
}

func terminalResolver(t interaction.Transition, requester events.Principal) events.Principal {
	switch {
	case t.To == interaction.InteractionStateExpired:
		return events.Principal{Kind: "service", ID: ExpiryActor.PrincipalRef, URN: "msg://service/tangent/" + urnSegment(ExpiryActor.PrincipalRef), Role: "system", Assurance: ExpiryActor.Assurance}
	case t.Cause == interaction.TerminalCauseCallerWithdrawn || t.Cause == interaction.TerminalCauseCallerCanceled:
		return requester
	}
	return actorPrincipal(t.ActorRef, t.Authority, "service")
}

func callerPrincipal(record interaction.InteractionRecord) events.Principal {
	ref := record.CallerPrincipalRef
	if ref == "" {
		ref = record.CallerScope
	}
	return events.Principal{Kind: "agent", ID: ref, URN: "msg://agent/tangent/" + urnSegment(ref), Assurance: record.CallerAssurance}
}

// actorPrincipal builds a principal from a journal actor. The local operator
// session is the one human actor Tangent knows.
func actorPrincipal(ref, authority, fallbackKind string) events.Principal {
	if ref == "" {
		ref = authority
	}
	if ref == "" {
		return events.Principal{}
	}
	if ref == OperatorParticipant.PrincipalRef {
		return events.Principal{Kind: "operator", ID: ref, URN: "msg://user/local/operator", Role: "operator"}
	}
	return events.Principal{Kind: fallbackKind, ID: ref, URN: "msg://" + urnKind(fallbackKind) + "/tangent/" + urnSegment(ref)}
}

func urnKind(kind string) string {
	if kind == "operator" {
		return "user"
	}
	return kind
}

// urnSegment keeps an opaque id URN-safe.
func urnSegment(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.', r == ':':
			return r
		}
		return '_'
	}, s)
}
