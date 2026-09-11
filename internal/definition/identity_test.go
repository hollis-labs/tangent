package definition_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/definition"
)

// The §3 rule, case by case. CW-20260911-0008 is the task that found the repo
// violating it; these are the cases a gate has to get right before it can be
// trusted to fail a build.

func publication(version string, revision int64, contract string) definition.Publication {
	return definition.Publication{
		Kind:     "tangent.app-board",
		Version:  version,
		Revision: revision,
		Identity: definition.Identity{
			ContractDigest:     contract,
			RendererClass:      definition.RendererReactComponent,
			RendererTrustClass: definition.TrustCoreTrusted,
		},
		ResponseSchema: definition.ResponseSchemaAbsent,
	}
}

func TestRevisionMayAdvanceWhileTheContractHolds(t *testing.T) {
	t.Parallel()
	// The case §3 keeps `revision` around for: a description or a renderer
	// asset corrected without the contract moving.
	before := publication("0.2", 1, "sha256:aaa")
	after := publication("0.2", 2, "sha256:aaa")
	if err := definition.CheckRevisionAdvance(before, after); err != nil {
		t.Fatalf("a revision bump over an unchanged contract was refused: %v", err)
	}
}

func TestRevisionMayNotAdvanceWhileTheContractMoves(t *testing.T) {
	t.Parallel()
	// This is exactly what tangent.app-board did three times.
	before := publication("0.1", 1, "sha256:aaa")
	after := publication("0.1", 2, "sha256:bbb")
	err := definition.CheckRevisionAdvance(before, after)
	if !errors.Is(err, definition.ErrIllegalRevisionAdvance) {
		t.Fatalf("error = %v, want ErrIllegalRevisionAdvance", err)
	}
	if !strings.Contains(err.Error(), "contract_digest") {
		t.Errorf("the failure does not name the field that moved: %v", err)
	}
}

func TestHoldingBothVersionAndRevisionWhileTheContractMovesIsRepublication(t *testing.T) {
	t.Parallel()
	// The other half of §3: the (publisher, kind, version) triple is immutable.
	// Distinguished from the case above because the remedy a reader reaches for
	// is different, and an error that says "advanced revision 1 → 1" would be
	// nonsense.
	before := publication("0.2", 1, "sha256:aaa")
	after := publication("0.2", 1, "sha256:bbb")
	err := definition.CheckRevisionAdvance(before, after)
	if !errors.Is(err, definition.ErrIllegalRevisionAdvance) {
		t.Fatalf("error = %v, want ErrIllegalRevisionAdvance", err)
	}
	if !strings.Contains(err.Error(), "republishes") {
		t.Errorf("the failure does not name republication: %v", err)
	}
}

func TestAVersionBumpCarriesAnyContractChange(t *testing.T) {
	t.Parallel()
	// What a version is for. The gate must not make a legal bump expensive, or
	// the next author will reach for a revision again.
	before := publication("0.1", 4, "sha256:aaa")
	after := publication("0.2", 1, "sha256:bbb")
	if err := definition.CheckRevisionAdvance(before, after); err != nil {
		t.Fatalf("a version bump carrying a moved contract was refused: %v", err)
	}
}

func TestATrustClassChangeIsAVersionBumpToo(t *testing.T) {
	t.Parallel()
	// §3 names four fields, not one. A renderer that quietly raises its trust
	// class under a revision bump is the change this clause is really about.
	before := publication("0.2", 1, "sha256:aaa")
	after := publication("0.2", 2, "sha256:aaa")
	after.Identity.RendererTrustClass = definition.TrustPortfolioTrusted
	err := definition.CheckRevisionAdvance(before, after)
	if !errors.Is(err, definition.ErrIllegalRevisionAdvance) {
		t.Fatalf("error = %v, want ErrIllegalRevisionAdvance", err)
	}
	if !strings.Contains(err.Error(), "renderer.trust_class") {
		t.Errorf("the failure does not name the field that moved: %v", err)
	}
}

func TestCapabilityOrderIsNotAnIdentityChange(t *testing.T) {
	t.Parallel()
	// A manifest that lists the same grants in a different order requests the
	// same authority. A gate that called that a contract move would be a gate
	// people learn to override.
	first := definition.CapabilitiesDigest([]definition.Capability{
		{ID: "export.download"}, {ID: "clipboard.write"},
	})
	second := definition.CapabilitiesDigest([]definition.Capability{
		{ID: "clipboard.write"}, {ID: "export.download"},
	})
	if first != second {
		t.Errorf("reordering required_capabilities moved the digest: %q vs %q", first, second)
	}
	if definition.CapabilitiesDigest(nil) != "" {
		t.Error("a definition requiring no capabilities should carry no capabilities digest")
	}
}

func TestTheResponseSchemaBackfillIsTheOneGrantedException(t *testing.T) {
	t.Parallel()
	// ADR 0003 §3, amended by A1. It is the only contract_digest change that
	// may ride a revision bump, and it is structurally once per kind.
	before := publication("0.6", 1, "sha256:aaa")
	after := publication("0.6", 2, "sha256:bbb")
	after.ResponseSchema = definition.ResponseSchemaPresent
	if err := definition.CheckRevisionAdvance(before, after); err != nil {
		t.Fatalf("the granted response-schema backfill was refused: %v", err)
	}
}

func TestTheBackfillExceptionCoversTheContractDigestAlone(t *testing.T) {
	t.Parallel()
	// A backfill is not a license to move everything else while nobody is
	// reading the diff. §8 C2 makes a trust-class change a version bump on its
	// own terms, and the exception does not reach it.
	before := publication("0.6", 1, "sha256:aaa")
	after := publication("0.6", 2, "sha256:bbb")
	after.ResponseSchema = definition.ResponseSchemaPresent
	after.Identity.RendererTrustClass = definition.TrustPortfolioTrusted
	err := definition.CheckRevisionAdvance(before, after)
	if !errors.Is(err, definition.ErrIllegalRevisionAdvance) {
		t.Fatalf("error = %v, want ErrIllegalRevisionAdvance", err)
	}
	if !strings.Contains(err.Error(), "renderer.trust_class") {
		t.Errorf("the failure does not name what moved beyond the exception: %v", err)
	}
}

func TestTheBackfillExceptionStillRequiresARevisionAdvance(t *testing.T) {
	t.Parallel()
	// "Advances revision and holds version" is the exception's own wording.
	// Holding both would republish different content under an immutable triple,
	// which is the thing §3 forbids in the first place.
	before := publication("0.6", 1, "sha256:aaa")
	after := publication("0.6", 1, "sha256:bbb")
	after.ResponseSchema = definition.ResponseSchemaPresent
	if err := definition.CheckRevisionAdvance(before, after); !errors.Is(err, definition.ErrIllegalRevisionAdvance) {
		t.Fatalf("error = %v, want ErrIllegalRevisionAdvance", err)
	}
}
