package mcp

import (
	"bytes"
	"regexp"
	"testing"
)

// The markdown inventory lives here, and this is what keeps it (CW-20260911-0004).
//
// Two schema surfaces describe the same fields: these tool schemas, which an
// agent reads while composing a call, and the ADR 0003 package request schemas
// the host validates against. Only these say which fields render as markdown.
//
// That is deliberate rather than half-finished. A package request schema's
// bytes are hashed into `contract_digest` and `binding_digest`, so a field
// `description` added there moves the pin — ADR 0003 §8 C1 makes every pending
// interaction of the kind `unavailable` for new submissions, and §3 turns the
// `revision` bump into the `version` bump §8 C3 freezes. A rendering fact must
// not be able to take a live surface out of service, so it lives on the
// surface where a correction costs a rebuild and nothing else.
// internal/envelope/extensions holds the other half: the package schemas stay
// silent, and a gate fails the build if one starts talking.

// renderingClaim matches the vocabulary these schemas settled on, in BOTH
// directions. "Displayed literally, NOT as markdown" is as much a rendering
// claim as "Renders as markdown." — and it is the half a reviewer comparing a
// diff hunk character by character depends on.
var renderingClaim = regexp.MustCompile(`(?i)(renders? as markdown|as markdown[,.]|not as markdown|stays? literal|displayed literally)`)

// sharedContextClaim is the one sentence every room-workflow schema carries,
// on the `context` field the shared wrapper adds. It is stripped before the
// check below, because a gate it satisfied would pass for a kind that had lost
// every claim about its OWN fields — which is the inventory that matters.
const sharedContextClaim = "Orienting prose shown above the workflow. Renders as markdown."

// TestEveryProseBearingSchemaSaysWhatRendersAsMarkdown names the kinds rather
// than counting them, and looks past the shared sentence they all carry.
//
// A count would pass while the wrong kind lost its inventory. `file-picker` is
// the one shipped workflow schema deliberately absent from this list: it
// carries no agent-authored prose field, so there is nothing for it to claim —
// and it is the control that proves this gate distinguishes the two.
func TestEveryProseBearingSchemaSaysWhatRendersAsMarkdown(t *testing.T) {
	t.Parallel()
	proseBearing := map[string][]byte{
		"tangent.app_board":          appBoardInputSchemaJSON,
		"tangent.approval_queue":     approvalQueueInputSchemaJSON,
		"tangent.block_draft":        blockDraftInputSchemaJSON,
		"tangent.dashboard":          dashboardInputSchemaJSON,
		"tangent.design_iteration":   designIterationInputSchemaJSON,
		"tangent.diff_review":        diffReviewInputSchemaJSON,
		"tangent.feedback":           feedbackInputSchemaJSON,
		"tangent.form_collect":       formCollectInputSchemaJSON,
		"tangent.interview_question": interviewQuestionInputSchemaJSON,
		"tangent.output_render":      outputRenderInputSchemaJSON,
		"tangent.progress_panel":     progressPanelInputSchemaJSON,
		"tangent.prose_revision":     proseRevisionInputSchemaJSON,
		"tangent.spreadsheet_review": spreadsheetReviewInputSchemaJSON,
		"tangent.synthesis_notes":    synthesisNotesInputSchemaJSON,
		"tangent.triage":             triageInputSchemaJSON,
		"tangent.whiteboard":         whiteboardInputSchemaJSON,
		"tangent.wizard":             wizardInputSchemaJSON,
	}
	for tool, schema := range proseBearing {
		own := bytes.ReplaceAll(schema, []byte(sharedContextClaim), nil)
		if !renderingClaim.Match(own) {
			t.Errorf("%s's input schema says nothing about markdown rendering.\n"+
				"It is the only surface that can: the ADR 0003 package request schema for "+
				"this kind is contract bytes and stays silent by design. An agent composing "+
				"this call now has to emit prose and read the result to find out.", tool)
		}
	}
	// The control. file-picker has no prose field of its own, so it must fail
	// the same check the seventeen above pass — otherwise the check is
	// matching something every schema carries and proving nothing.
	own := bytes.ReplaceAll(filePickerInputSchemaJSON, []byte(sharedContextClaim), nil)
	if renderingClaim.Match(own) {
		t.Error("tangent.file_picker now claims a field renders as markdown.\n" +
			"It was this gate's control: a kind with no agent-authored prose. If it " +
			"has gained one, add it to the list above rather than deleting this check.")
	}
}
