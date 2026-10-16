// Package gohitl runs the go-hitl reference Service (libs/mesh/core/hitl) over
// Tangent's own interaction tables, behind the tangent_hitl_* MCP tools.
//
// It is the runtime half of CW-20260224-0031 / CW-20261016-0153: sqlstore
// (internal/hitl/sqlstore) is the persistence boundary, this package is the
// wire the tools speak. Tangent stays the queue and the operator UI: the rows
// are ordinary HITL items, so the inbox shows them and Tangent's own Resolve
// path (the operator, elevated per CW-20261016-0174) resolves them. The lib
// Service sees every outcome because its store reads the same rows.
//
// Off by default. TANGENT_HITL_BACKEND=gohitl turns it on; unset or "native"
// keeps Tangent's own internal/hitl.Service behind the tools. Rollback is
// unsetting the variable and restarting: no data moves, the rows are native.
package gohitl

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	libhitl "github.com/hollis-labs/libs/mesh/core/hitl"

	"github.com/hollis-labs/tangent/internal/hitl/sqlstore"
	"github.com/hollis-labs/tangent/internal/interaction"
)

// EnvBackend names the variable that selects the backend behind tangent_hitl_*.
const EnvBackend = "TANGENT_HITL_BACKEND"

// Backend names.
const (
	BackendNative = "native"
	BackendGoHITL = "gohitl"
)

// Enabled reports whether value selects the go-hitl backend. Empty and
// "native" are the default; anything else unknown is an error so a typo does
// not silently leave the flag off.
func Enabled(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", BackendNative:
		return false, nil
	case BackendGoHITL:
		return true, nil
	}
	return false, fmt.Errorf("%s=%q: want %q or %q", EnvBackend, value, BackendNative, BackendGoHITL)
}

// Wire is the go-hitl Service behind the tangent_hitl_* tools. Each method takes
// the tool's JSON document and returns the wire JSON, exactly as the lib's own
// conformance adapter does, so the tool contract is the go-hitl wire.
type Wire struct {
	svc *libhitl.Service
}

// New returns a Wire over a migrated Tangent database. catalog resolves the
// HITL item definition each row is pinned to.
func New(db *sql.DB, catalog interaction.DefinitionCatalog) *Wire {
	return &Wire{svc: libhitl.NewService(sqlstore.New(db, catalog), libhitl.Options{})}
}

// Service exposes the underlying lib Service (for tests and the sweeper).
func (w *Wire) Service() *libhitl.Service { return w.svc }

// Enqueue implements the tangent_hitl_enqueue wire.
func (w *Wire) Enqueue(ctx context.Context, doc []byte) ([]byte, error) {
	var req libhitl.EnqueueRequest
	if err := json.Unmarshal(doc, &req); err != nil {
		return reply(nil, libhitl.NewInvalidRequest(err))
	}
	handle, err := w.svc.Enqueue(ctx, req)
	return reply(handle, err)
}

// Get implements the tangent_hitl_get wire.
func (w *Wire) Get(ctx context.Context, doc []byte) ([]byte, error) {
	cmd, err := libhitl.DecodeGetCommand(doc)
	if err != nil {
		return reply(nil, err)
	}
	result, err := w.svc.Get(ctx, cmd)
	return reply(result, err)
}

// Await implements the tangent_hitl_await wire.
func (w *Wire) Await(ctx context.Context, doc []byte) ([]byte, error) {
	cmd, err := libhitl.DecodeAwaitCommand(doc)
	if err != nil {
		return reply(nil, err)
	}
	result, err := w.svc.Await(ctx, cmd)
	return reply(result, err)
}

// Withdraw implements the tangent_hitl_withdraw wire.
func (w *Wire) Withdraw(ctx context.Context, doc []byte) ([]byte, error) {
	cmd, err := libhitl.DecodeWithdrawCommand(doc)
	if err != nil {
		return reply(nil, err)
	}
	outcome, err := w.svc.Withdraw(ctx, cmd)
	return reply(outcome, err)
}

// reply encodes a result, or the error as the go-hitl wire error document.
func reply(value any, err error) ([]byte, error) {
	if err != nil {
		return libhitl.EncodeError(err), err
	}
	raw, marshalErr := json.Marshal(value)
	return raw, marshalErr
}
