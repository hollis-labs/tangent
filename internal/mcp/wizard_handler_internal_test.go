package mcp

import (
	envelopes "github.com/hollis-labs/go-envelopes"
	"testing"

	"github.com/hollis-labs/tangent/internal/room"
)

func TestBuildVisibleWizardEnvelopePreservesTopLevelTitleWhenViewFieldsBlank(t *testing.T) {
	env := &envelopes.Envelope{
		ID:    "wizard-1",
		Type:  wizardEnvelopeType,
		Title: "Release wizard",
		Data: map[string]any{
			"title":       "Release wizard",
			"description": "Top-level description",
		},
	}

	got := buildVisibleWizardEnvelope(env, &room.WizardStateView{
		WizardID:      "wizard-1",
		Title:         "",
		Description:   "",
		CurrentStepID: "step-1",
		Steps:         []room.WizardStep{{StepID: "step-1", Title: "Step 1"}},
		UpdatedAt:     "2026-05-10T00:00:00Z",
	})

	if got.Data["title"] != "Release wizard" {
		t.Fatalf("data.title = %v, want Release wizard", got.Data["title"])
	}
	if got.Data["description"] != "Top-level description" {
		t.Fatalf("data.description = %v, want Top-level description", got.Data["description"])
	}
}
