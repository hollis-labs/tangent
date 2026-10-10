package ws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hollis-labs/tangent/internal/room"
	"github.com/hollis-labs/tangent/internal/uicommand"
)

// UIAttachmentResolver is a trusted host adapter, not a wire assertion. It
// must bind the authenticated participant session/conversation to this exact
// server-issued attachment, using a non-credential session reference. The
// ordinary room ParticipantResolver/lease does not establish this authority.
type UIAttachmentResolver func(*http.Request, *room.Connection) (uicommand.Binding, error)

// SetUICommands installs the host channel before serving requests. A nil
// resolver refuses attachment. Production deliberately supplies neither until
// a verified conversation/session binding provider is available.
func (h *Handler) SetUICommands(broker *uicommand.Broker, resolver UIAttachmentResolver) {
	h.uiBroker = broker
	h.uiBinding = resolver
}
func (h *Handler) attachUI(r *http.Request, c *room.Connection) {
	if h.uiBroker == nil || h.uiBinding == nil {
		return
	}
	binding, err := h.uiBinding(r, c)
	if err != nil {
		return
	}
	_ = h.uiBroker.Attach(c.ID(), binding, func(ctx context.Context, frame uicommand.Frame) error { return c.WriteJSON(ctx, frame) })
}
func (h *Handler) dispatchUI(ctx context.Context, c *room.Connection, raw []byte) bool {
	var header struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &header) != nil {
		return false
	}
	switch header.Type {
	case "view.publish", "view.active", "ui.control", "ui.ack":
	default:
		return false
	}
	err := uicommand.ErrForbidden
	if h.uiBroker != nil {
		switch header.Type {
		case "view.publish":
			var msg struct {
				Type       string          `json:"type"`
				Descriptor json.RawMessage `json:"descriptor"`
			}
			if err = uicommand.DecodeFrame(raw, &msg); err == nil {
				var revision uint64
				revision, err = h.uiBroker.Publish(c.ID(), msg.Descriptor)
				if err == nil {
					err = c.WriteJSON(ctx, struct {
						Type     string `json:"type"`
						Revision uint64 `json:"view_revision"`
					}{"view.published", revision})
				}
			}
		case "view.active":
			var msg struct {
				Type   string `json:"type"`
				Active *bool  `json:"active"`
			}
			if err = uicommand.DecodeFrame(raw, &msg); err == nil {
				if msg.Active == nil {
					err = errors.New("active is required")
				} else {
					err = h.uiBroker.Active(c.ID(), *msg.Active)
				}
			}
		case "ui.control":
			var msg struct {
				Type    string `json:"type"`
				Enabled *bool  `json:"enabled"`
			}
			if err = uicommand.DecodeFrame(raw, &msg); err == nil {
				if msg.Enabled == nil {
					err = errors.New("enabled is required")
				} else {
					err = h.uiBroker.SetControl(c.ID(), *msg.Enabled)
				}
			}
		case "ui.ack":
			var msg struct {
				Type string        `json:"type"`
				Ack  uicommand.Ack `json:"ack"`
			}
			if err = uicommand.DecodeFrame(raw, &msg); err == nil {
				err = h.uiBroker.Acknowledge(c.ID(), msg.Ack)
			}
		}
	}
	if err != nil {
		h.sendError(ctx, c, outboundError{Code: "ui_command_rejected", Message: err.Error(), ConnectionID: c.ID()})
	}
	return true
}
