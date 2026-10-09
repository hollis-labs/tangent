package envelope_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/libs/ui-go/envelopes/envelopestest"
	"github.com/hollis-labs/tangent/internal/envelope"
)

// TestEnvelopesContract is the consumer-side gate against go-envelopes
// regressions. It exercises the v1 API invariants (validate-success,
// unknown-type error, plugin extension round-trip, core-type protection,
// canonical response kinds) via the shared envelopestest.RunContract
// suite. Failure here means a go-envelopes upgrade has broken Tangent's
// integration assumptions — investigate before bumping the dep.
//
// Per the migration recipe (go-envelopes docs/migration-from-nanite.md
// §"Tangent (next consumer)"), every consumer should run this test.
func TestEnvelopesContract(t *testing.T) {
	svc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	envelopestest.RunContract(t, svc.Registry())
}
