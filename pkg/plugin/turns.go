package plugin

import (
	"bytes"
	_ "embed"
)

// AgentTurnContractVersion is the payload contract version of the
// tangent.agent-turn kind a plugin enqueues through tangent.turns_enqueue
// (docs/contracts/agent-turns-inbox-v1.md). The host owns the kind and aliases
// this constant; it lives here so a plugin and the host agree by construction.
const AgentTurnContractVersion = "1.1"

//go:embed turns_enqueue.schema.json
var turnsEnqueueInputSchema []byte

// TurnsEnqueueInputSchema returns the JSON Schema tangent.turns_enqueue
// accepts: the tangent.agent-turn request schema, which the host owns and
// ships in its package tree. A plugin that enqueues turns validates what it
// builds against this in its own tests, so the contract with the host is a
// published schema rather than an import of host code.
//
// This file is a copy, because an embed cannot reach the host's package tree.
// internal/mcp's TestTurnsEnqueueAdvertisesThePublishedSchema holds the copy,
// the packaged schema and the live tool's advertised schema equal.
func TurnsEnqueueInputSchema() []byte {
	return bytes.Clone(turnsEnqueueInputSchema)
}
