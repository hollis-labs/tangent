package effect

import (
	"context"
	"testing"

	"github.com/hollis-labs/tangent/internal/authz"
	"github.com/hollis-labs/tangent/internal/definition"
)

// Mediation is now a function of two things, and this file is where that claim
// is held to account.
//
// CW-20260904-0077 reported three capabilities it could not enforce and said
// they would become real "only behind CW-20260825-0073's sandboxed trust
// classes + CSP". Two of the three did, and only in one isolation. Writing the
// whole table out is what stops a future edit from quietly promoting the other
// cells.

func TestMediationIsAFunctionOfCapabilityAndIsolation(t *testing.T) {
	t.Parallel()
	isolations := []Isolation{
		IsolationMainOrigin, IsolationHostPrimitive,
		IsolationSandboxedFrame, IsolationExternalSurface,
	}
	// Written as the full grid rather than as rules, so a change has to be
	// made here deliberately rather than fall out of an edit elsewhere.
	want := map[Capability]map[Isolation]Mediation{
		FileReadScoped: {
			IsolationMainOrigin: MediationHost, IsolationHostPrimitive: MediationHost,
			IsolationSandboxedFrame: MediationHost, IsolationExternalSurface: MediationHost,
		},
		FileWriteScoped: {
			IsolationMainOrigin: MediationHost, IsolationHostPrimitive: MediationHost,
			IsolationSandboxedFrame: MediationHost, IsolationExternalSurface: MediationHost,
		},
		EvidencePreview: {
			IsolationMainOrigin: MediationHost, IsolationHostPrimitive: MediationHost,
			IsolationSandboxedFrame: MediationHost, IsolationExternalSurface: MediationHost,
		},
		// `connect-src 'self'` on the document is what changed this row. It is
		// host-mediated in *every* isolation, because no renderer in any of
		// them can reach an external origin any more.
		NetworkFetch: {
			IsolationMainOrigin: MediationHost, IsolationHostPrimitive: MediationHost,
			IsolationSandboxedFrame: MediationHost, IsolationExternalSurface: MediationHost,
		},
		// These two are the honest half. There is no CSP directive for either,
		// so in Tangent's own origin the browser still hands them over; inside
		// an opaque-origin frame with no `allow-downloads` and a
		// `clipboard-write=(self)` permissions policy, it does not.
		ExportDownload: {
			IsolationMainOrigin: MediationDeclared, IsolationHostPrimitive: MediationHost,
			IsolationSandboxedFrame: MediationHost, IsolationExternalSurface: MediationHost,
		},
		ClipboardWrite: {
			IsolationMainOrigin: MediationDeclared, IsolationHostPrimitive: MediationHost,
			IsolationSandboxedFrame: MediationHost, IsolationExternalSurface: MediationHost,
		},
		ProcessExec: {
			IsolationMainOrigin: MediationUnimplemented, IsolationHostPrimitive: MediationUnimplemented,
			IsolationSandboxedFrame: MediationUnimplemented, IsolationExternalSurface: MediationUnimplemented,
		},
	}

	for _, capability := range Capabilities() {
		row, ok := want[capability]
		if !ok {
			t.Fatalf("%s has no expectation; a new capability needs a row in this grid", capability)
		}
		for _, isolation := range isolations {
			if got := MediationFor(capability, isolation); got != row[isolation] {
				t.Errorf("MediationFor(%s, %s) = %s, want %s",
					capability, isolation, got, row[isolation])
			}
		}
	}
}

// An unset isolation must never buy a stronger claim than the model can prove.
func TestTheZeroIsolationIsTheWeakestAnswer(t *testing.T) {
	t.Parallel()
	for _, capability := range Capabilities() {
		zero := MediationFor(capability, "")
		if zero != MediationFor(capability, IsolationMainOrigin) {
			t.Errorf("%s: the zero isolation answers %s, main-origin answers %s; "+
				"an unrecorded isolation must read as the least enforced one",
				capability, zero, MediationFor(capability, IsolationMainOrigin))
		}
		if got := MediationOf(capability); got != zero {
			t.Errorf("%s: MediationOf = %s, MediationFor(main-origin) = %s", capability, got, zero)
		}
	}
	if MediationFor(Capability("something.new"), IsolationSandboxedFrame) != MediationUnimplemented {
		t.Error("an unknown capability resolved to something other than unimplemented")
	}
}

// The two packages keep separate vocabularies on purpose — internal/definition
// depends on nothing but the standard library and a YAML parser — so the
// agreement is asserted rather than enforced by the compiler.
func TestIsolationVocabulariesAgree(t *testing.T) {
	t.Parallel()
	pairs := []struct {
		here  Isolation
		there definition.Isolation
	}{
		{IsolationMainOrigin, definition.IsolationMainOrigin},
		{IsolationHostPrimitive, definition.IsolationHostPrimitive},
		{IsolationSandboxedFrame, definition.IsolationSandboxedFrame},
		{IsolationExternalSurface, definition.IsolationExternalSurface},
	}
	for _, pair := range pairs {
		if string(pair.here) != string(pair.there) {
			t.Errorf("effect.%s and definition.%s are spelled differently", pair.here, pair.there)
		}
	}
	seen := map[Isolation]bool{}
	for _, isolation := range definition.Isolations() {
		seen[Isolation(isolation)] = true
	}
	for _, pair := range pairs {
		if !seen[pair.here] {
			t.Errorf("definition implements no profile for %q; either the value is dead "+
				"or a position is missing", pair.here)
		}
	}
}

// The class-based capability ceiling this used to hold is gone (ADR 0009).
//
// It asserted that internal/definition's ceiling literals named only
// capabilities this build knows, and that every cataloged capability was
// reachable by some class. Both directions were real while the ceiling was a
// gate; with the ceiling removed there is no second list to keep honest, and a
// test that walked an empty one would pass by vacancy.
//
// What replaced it as the thing worth pinning is TestIsolationVocabulariesAgree
// above — the two packages still spell `Isolation` separately, and that pair
// still has to match.

// A receipt has to say which isolation its mediation was decided in, or the
// mediation column is not interpretable. Asserted through the broker rather
// than on the struct, because the wiring is the part that breaks.
func TestReceiptsRecordTheIsolationTheDecisionWasMadeIn(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)

	request := func(isolation Isolation, key string) Receipt {
		return fixture.request(t, Request{
			Capability: ClipboardWrite,
			Principal:  fixture.participant(),
			Binding: Binding{
				BindingDigest: fixture.digest,
				Required:      []Capability{ClipboardWrite},
				Granted:       []Capability{ClipboardWrite},
				Isolation:     isolation,
			},
			OwnerScope:     authz.ParticipantScope,
			InteractionID:  fixture.interactionID,
			IdempotencyKey: key,
			Intent:         confirmedIntent(),
		}).Receipt
	}

	main := request(IsolationMainOrigin, "trust-main")
	if main.Decision != DecisionGranted {
		t.Fatalf("main-origin decision = %s (%s), want granted", main.Decision, main.Code)
	}
	if main.Mediation != MediationDeclared {
		t.Errorf("main-origin mediation = %s, want declared: the browser still hands a "+
			"same-origin renderer navigator.clipboard", main.Mediation)
	}
	if main.Isolation != IsolationMainOrigin {
		t.Errorf("receipt lost its isolation: %q", main.Isolation)
	}

	sandboxed := request(IsolationSandboxedFrame, "trust-sandboxed")
	// No performer is registered for clipboard.write, so in an isolation where
	// the host is the only possible actor the honest answer is `unavailable` —
	// not an admission that leaves the renderer to perform it.
	if sandboxed.Decision != DecisionRefused || sandboxed.Code != CodeUnavailable {
		t.Errorf("sandboxed decision = %s/%s, want refused/%s",
			sandboxed.Decision, sandboxed.Code, CodeUnavailable)
	}
	if sandboxed.Mediation != MediationHost {
		t.Errorf("sandboxed mediation = %s, want host", sandboxed.Mediation)
	}
	if sandboxed.Isolation != IsolationSandboxedFrame {
		t.Errorf("sandboxed receipt isolation = %q", sandboxed.Isolation)
	}

	// And the audit trail survives a round trip through the store, which is the
	// half a struct-only assertion would miss.
	store, err := NewSQLStore(fixture.db)
	if err != nil {
		t.Fatalf("NewSQLStore: %v", err)
	}
	stored, _, found, err := store.ReceiptForKey(context.Background(), "trust-sandboxed", ClipboardWrite)
	if err != nil || !found {
		t.Fatalf("ReceiptForKey: found=%v err=%v", found, err)
	}
	if stored.Isolation != IsolationSandboxedFrame {
		t.Errorf("persisted receipt isolation = %q, want the value it was written with",
			stored.Isolation)
	}
	// The legacy trust-class column is empty on receipts minted after ADR 0009
	// removed the class. Rows written before it keep whatever they recorded —
	// migration 0010's own note is that rewriting an audit trail would be worse
	// than a blank that reads as "not recorded".
	if stored.TrustClass != "" {
		t.Errorf("a receipt minted after ADR 0009 carries a trust class %q", stored.TrustClass)
	}
}
