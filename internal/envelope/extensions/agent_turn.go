package extensions

import (
	"bytes"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/pkg/plugin"
)

// TurnsPackageID is the package tangent.agent-turn ships in.
const TurnsPackageID = "tangent.turns"

// AgentTurnEnvelopeType is the immutable definition kind used by agent turns in the
// second durable operator-owned FIFO inbox (CW-20260913-0019).
const AgentTurnEnvelopeType = "tangent.agent-turn"

// AgentTurnContractVersion is the payload contract version. It is defined in
// pkg/plugin, because the runner plugin enqueues turns against it.
const AgentTurnContractVersion = plugin.AgentTurnContractVersion

// AgentTurnDefinitionVersion is the version of the shipped manifest.
const AgentTurnDefinitionVersion = "1.0"

var agentTurnSchema = mustPackageFile(TurnsPackageID, AgentTurnEnvelopeType, requestSchemaFileName)

// AgentTurnContractSchema returns a copy of the turn request schema bundle.
func AgentTurnContractSchema() []byte {
	return bytes.Clone(agentTurnSchema)
}

// RegisterAgentTurn registers the agent turn request definition on svc.
func RegisterAgentTurn(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, TurnsPackageID, AgentTurnEnvelopeType)
}
