package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/interaction"
)

type inboxListInput struct {
	RequesterScope string                       `json:"requester_scope"`
	Kind           string                       `json:"kind,omitempty"`
	Status         interaction.InteractionState `json:"status,omitempty"`
	CreatedFrom    string                       `json:"created_from,omitempty"`
	CreatedBefore  string                       `json:"created_before,omitempty"`
	Limit          int                          `json:"limit,omitempty"`
	Cursor         string                       `json:"cursor,omitempty"`
}

type inboxSearchInput struct {
	RequesterScope string                       `json:"requester_scope"`
	Query          string                       `json:"query"`
	Kind           string                       `json:"kind,omitempty"`
	Status         interaction.InteractionState `json:"status,omitempty"`
	CreatedFrom    string                       `json:"created_from,omitempty"`
	CreatedBefore  string                       `json:"created_before,omitempty"`
	Limit          int                          `json:"limit,omitempty"`
	Cursor         string                       `json:"cursor,omitempty"`
}

type inboxGetInput struct {
	RequesterScope string `json:"requester_scope"`
	ItemID         string `json:"item_id"`
}

func (s *Server) registerInboxReadTools() error {
	description := "Read only caller-owned or surface-owned inbox items. The host derives authority from admission; requester_scope is a declared local partition, never an operator or gateway grant. " + authz.AdvisoryPartitionNotice + " "
	if err := addInteractionTool(s, "tangent.inbox_list", description+"List retained visible item metadata in arrival order with kind/status/creation-window filters and cursor pagination; no bodies or global counts.", s.handleInboxList); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.inbox_search", description+"Search retained request and confirmed response JSON text values by literal case-insensitive substring. Private drafts, delivery metadata and redaction tombstones are excluded. Returns metadata with the same bounded pagination.", s.handleInboxSearch); err != nil {
		return err
	}
	return addInteractionTool(s, "tangent.inbox_get", description+"Read one item and its immutable confirmed response history. Erased content remains a tombstone; hidden/purged items are not found. Does not present, retrieve, deliver or acknowledge an outcome.", s.handleInboxGet)
}

func inboxReadArguments(input inboxListInput) (interaction.InboxReadInput, error) {
	result := interaction.InboxReadInput{RequesterScope: requesterScope(input.RequesterScope), Kind: input.Kind, Status: input.Status, Limit: input.Limit, Cursor: input.Cursor}
	for _, field := range []struct {
		name, value string
		target      **time.Time
	}{
		{"created_from", input.CreatedFrom, &result.CreatedFrom}, {"created_before", input.CreatedBefore, &result.CreatedBefore},
	} {
		if field.value == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, field.value)
		if err != nil {
			return result, fmt.Errorf("%w: %s must be RFC3339", interaction.ErrInvalidRecord, field.name)
		}
		parsed = parsed.UTC()
		*field.target = &parsed
	}
	return result, nil
}

func (s *Server) handleInboxList(ctx context.Context, input inboxListInput) (any, error) {
	args, err := inboxReadArguments(input)
	if err != nil {
		return nil, s.interactionError(err)
	}
	return s.interactionResult(s.interactions.ReadInbox(ctx, args))
}

func (s *Server) handleInboxSearch(ctx context.Context, input inboxSearchInput) (any, error) {
	args, err := inboxReadArguments(inboxListInput{RequesterScope: input.RequesterScope, Kind: input.Kind, Status: input.Status, CreatedFrom: input.CreatedFrom, CreatedBefore: input.CreatedBefore, Limit: input.Limit, Cursor: input.Cursor})
	if err != nil {
		return nil, s.interactionError(err)
	}
	if strings.TrimSpace(input.Query) == "" {
		return nil, s.interactionError(fmt.Errorf("%w: query is required", interaction.ErrInvalidRecord))
	}
	args.Query = input.Query
	return s.interactionResult(s.interactions.ReadInbox(ctx, args))
}

func (s *Server) handleInboxGet(ctx context.Context, input inboxGetInput) (any, error) {
	return s.interactionResult(s.interactions.GetInboxItem(ctx, requesterScope(input.RequesterScope), input.ItemID))
}
