package interaction

// Assurance-ordered presenter takeover (CW-20261016-0174).
//
// The store binds a presented interaction to the participant that presented
// it, and only that participant may resolve it. Before operator elevation
// existed, every browser presentation — and every docs/turns auto-presentation
// — was made by the `loopback-unverified` local operator. After it, resolving
// requires an `operator-token` session, which is a different binding. Without
// a takeover rule every item presented before the upgrade would be
// unresolvable.
//
// The rule is deliberately one-directional: a resolver with strictly higher
// assurance, in the same participant scope, may supersede the presenter. A
// lower or equal assurance never may, so an unverified session cannot steal
// an item an operator presented, and two distinct identities at the same
// assurance keep the original single-presenter behavior. The takeover is
// journaled as EventPresenterSuperseded with both participant refs.

// EventPresenterSuperseded is the interaction_events type recorded when a
// higher-assurance participant resolves an item another identity presented.
const EventPresenterSuperseded = "interaction.presenter_superseded"

// assuranceRanks orders the participant assurances a takeover may compare.
// An assurance not listed ranks zero and can neither supersede nor be
// superseded: an unknown value is never evidence of anything.
var assuranceRanks = map[string]int{
	"asserted":            1,
	"loopback-unverified": 1,
	"operator-token":      2,
}

// AssuranceRank returns the takeover rank of a participant assurance, or zero
// for one this build does not order.
func AssuranceRank(assurance string) int { return assuranceRanks[assurance] }

// CanSupersedePresenter reports whether actor may resolve an interaction that
// a different identity presented: same participant scope, a presenter with a
// known assurance, and a resolver whose assurance ranks strictly higher.
func CanSupersedePresenter(record InteractionRecord, actor ActorBinding) bool {
	if record.ParticipantScope == "" || record.ParticipantScope != actor.Scope {
		return false
	}
	presenter := AssuranceRank(record.ParticipantAssurance)
	return presenter > 0 && AssuranceRank(actor.Assurance) > presenter
}
