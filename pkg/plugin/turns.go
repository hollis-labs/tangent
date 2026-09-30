package plugin

// AgentTurnContractVersion is the payload contract version of the
// tangent.agent-turn kind a plugin enqueues through tangent.turns_enqueue
// (docs/contracts/agent-turns-inbox-v1.md). The host owns the kind and aliases
// this constant; it lives here so a plugin and the host agree by construction.
const AgentTurnContractVersion = "1.0"
