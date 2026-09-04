package extensions

import (
	_ "embed"
	"fmt"

	"github.com/hollis-labs/tangent/internal/envelope"
)

const SpreadsheetReviewEnvelopeType = "tangent.spreadsheet-review"

var spreadsheetReviewManifest = []byte(`type: tangent.spreadsheet-review
version: "0.5"
description: "Spreadsheet review envelope: render canonical agent-provided rows in a persistent room with explicit submit, saved views, and CSV export metadata."
responseKind: data
ui:
  component: SpreadsheetReviewView
`)

//go:embed spreadsheet_review_schema.json
var spreadsheetReviewSchema []byte

func RegisterSpreadsheetReview(svc *envelope.Service) error {
	if svc == nil {
		return fmt.Errorf("extensions: envelope service is nil")
	}
	if err := svc.RegisterTypeFromManifest(SpreadsheetReviewEnvelopeType, spreadsheetReviewManifest, spreadsheetReviewSchema, PluginID); err != nil {
		return fmt.Errorf("extensions: register spreadsheet-review: %w", err)
	}
	return nil
}
