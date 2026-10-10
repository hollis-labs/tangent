package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	gmcpserver "github.com/hollis-labs/libs/plugin-mcp/go-mcp/server"
	"github.com/hollis-labs/tangent/internal/uicommand"
)

// WithUICommands injects the shared host broker before serving. Its trusted
// authorizer must derive verified bindings from context, never tool arguments.
// Production has no verified binding provider and leaves this seam unavailable.
func WithUICommands(broker *uicommand.Broker) Option {
	return func(s *Server) error {
		if broker == nil {
			return fmt.Errorf("mcp: UI command broker is nil")
		}
		s.uiCommands = broker
		return nil
	}
}

func (s *Server) registerUICommandTools() error {
	empty, err := buildSchema([]byte(`{"type":"object","properties":{},"additionalProperties":false}`), "view_get")
	if err != nil {
		return err
	}
	addTool(s, gmcpserver.Tool{Name: "tangent.view_get", Description: "Read the caller's fresh participant view. Requires a verified participant/conversation/browser binding; unavailable in production until that provider is installed. No targeting arguments.", InputSchema: empty}, s.handleViewGet)
	// Only reviewed core definitions become tools. Descriptive schemas supplied
	// by a browser or plugin never install effectful MCP handlers.
	for _, declaration := range uicommand.CoreCommands() {
		name := declaration.Name
		schema, err := buildSchema(declaration.Schema, name)
		if err != nil {
			return err
		}
		addTool(s, gmcpserver.Tool{Name: "tangent.ui_" + name, Description: "Apply the declared " + name + " presentation command to the verified caller's active browser. Requires participant control opt-in and current view declaration. Returns browser acknowledgement; timeout does not prove the command was unapplied. Production binding is unavailable.", InputSchema: schema}, func(ctx context.Context, args map[string]any) (any, error) {
			return s.handleUICommand(ctx, name, args)
		})
	}
	return nil
}

func (s *Server) handleViewGet(ctx context.Context, _ map[string]any) (any, error) {
	if s.uiCommands == nil {
		return nil, uiUnavailable()
	}
	snapshot, err := s.uiCommands.Get(ctx)
	if err != nil {
		return nil, uiCommandError(err)
	}
	return snapshot, nil
}

func (s *Server) handleUICommand(ctx context.Context, name string, args map[string]any) (any, error) {
	if s.uiCommands == nil {
		return nil, uiUnavailable()
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, toolErrorResult("ui_rejected", "invalid command arguments")
	}
	ack, err := s.uiCommands.Command(ctx, name, raw)
	if err != nil {
		return nil, uiCommandError(err)
	}
	return ack, nil
}

func uiUnavailable() error {
	return toolErrorResult("ui_unavailable", "verified participant/conversation/browser binding provider is unavailable")
}

func uiCommandError(err error) error {
	code, message := "ui_rejected", "UI command refused"
	switch {
	case errors.Is(err, uicommand.ErrForbidden):
		code, message = "ui_forbidden", "verified UI access required"
	case errors.Is(err, uicommand.ErrNotVisible):
		code, message = "not_visible", "no active participant view"
	case errors.Is(err, uicommand.ErrDisabled):
		code, message = "ui_disabled", "participant disabled agent control"
	case errors.Is(err, uicommand.ErrTimeout):
		code, message = "timeout", "browser acknowledgement timed out; application outcome is unknown"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		code, message = "ui_canceled", "UI command wait canceled; application outcome is unknown"
	}
	return toolErrorResult(code, message)
}
