package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/hitl"
)

type fakeHITLEvidenceService struct {
	*fakeHITLService
	reference hitl.TangentReferenceEvidenceView
	preview   hitl.ArtifactPreview
	err       error
	itemID    string
	index     int
}

func (f *fakeHITLEvidenceService) InspectTangentEvidence(
	_ context.Context,
	itemID string,
	index int,
) (hitl.TangentReferenceEvidenceView, error) {
	f.itemID, f.index = itemID, index
	return f.reference, f.err
}

func (f *fakeHITLEvidenceService) PreviewArtifactEvidence(
	_ context.Context,
	itemID string,
	index int,
) (hitl.ArtifactPreview, error) {
	f.itemID, f.index = itemID, index
	return f.preview, f.err
}

func TestHITLEvidenceRoutesUseStoredItemAndIndex(t *testing.T) {
	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	fake := &fakeHITLEvidenceService{
		fakeHITLService: &fakeHITLService{},
		reference: hitl.TangentReferenceEvidenceView{
			Status: "available", Label: "Earlier review",
		},
		preview: hitl.ArtifactPreview{Status: "available", Kind: "text", Content: "checks passed"},
	}
	srv, err := New(Config{Envelope: envelopeService, HITL: fake})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://localhost:7842/api/hitl/items/item-1/evidence/3/reference", nil)
	response := httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || fake.itemID != "item-1" || fake.index != 3 ||
		!strings.Contains(response.Body.String(), `"label":"Earlier review"`) {
		t.Fatalf("reference response = %d %s; args=%s/%d", response.Code, response.Body.String(), fake.itemID, fake.index)
	}

	request = httptest.NewRequest(http.MethodGet, "http://localhost:7842/api/hitl/items/item-1/evidence/4/preview", nil)
	response = httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || fake.index != 4 || !strings.Contains(response.Body.String(), `"content":"checks passed"`) {
		t.Fatalf("preview response = %d %s; index=%d", response.Code, response.Body.String(), fake.index)
	}
}

func TestHITLEvidenceRoutesReturnUsefulBoundedFailureStates(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "missing", err: hitl.ErrEvidenceMissing, status: http.StatusNotFound, code: "evidence_missing"},
		{name: "expired", err: hitl.ErrEvidenceExpired, status: http.StatusGone, code: "evidence_expired"},
		{name: "unauthorized", err: hitl.ErrEvidenceUnauthorized, status: http.StatusForbidden, code: "evidence_unauthorized"},
		{name: "unsupported", err: hitl.ErrEvidenceUnsupported, status: http.StatusUnprocessableEntity, code: "evidence_unsupported"},
		{name: "too large", err: hitl.ErrEvidenceTooLarge, status: http.StatusRequestEntityTooLarge, code: "evidence_too_large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelopeService, err := envelope.New(context.Background())
			if err != nil {
				t.Fatalf("envelope.New: %v", err)
			}
			fake := &fakeHITLEvidenceService{fakeHITLService: &fakeHITLService{}, err: test.err}
			srv, err := New(Config{Envelope: envelopeService, HITL: fake})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			request := httptest.NewRequest(http.MethodGet, "http://localhost:7842/api/hitl/items/item-1/evidence/0/reference", nil)
			response := httptest.NewRecorder()
			srv.mux.ServeHTTP(response, request)
			if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}

	envelopeService, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	srv, err := New(Config{Envelope: envelopeService, HITL: &fakeHITLService{}})
	if err != nil {
		t.Fatalf("New without evidence service: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://localhost:7842/api/hitl/items/item-1/evidence/0/preview", nil)
	response := httptest.NewRecorder()
	srv.mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "evidence_unsupported") {
		t.Fatalf("optional evidence response = %d %s", response.Code, response.Body.String())
	}
}
