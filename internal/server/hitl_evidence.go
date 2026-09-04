package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/hollis-labs/tangent/internal/hitl"
)

// hitlEvidenceService is optional so the browser's core queue interface stays
// narrow. Production's HITL service implements it; lightweight host adapters
// may intentionally omit evidence capabilities.
type hitlEvidenceService interface {
	InspectTangentEvidence(context.Context, string, int) (hitl.TangentReferenceEvidenceView, error)
	PreviewArtifactEvidence(context.Context, string, int) (hitl.ArtifactPreview, error)
}

func (h *hitlHTTPHandler) tangentEvidence(w http.ResponseWriter, r *http.Request) {
	service, ok := h.service.(hitlEvidenceService)
	if !ok {
		writeHITLEvidenceError(w, hitl.ErrEvidenceUnsupported)
		return
	}
	index, err := hitlEvidenceIndex(r)
	if err != nil {
		writeHITLEvidenceError(w, err)
		return
	}
	view, err := service.InspectTangentEvidence(r.Context(), r.PathValue("itemID"), index)
	if err != nil {
		writeHITLEvidenceError(w, err)
		return
	}
	writeHITLJSON(w, http.StatusOK, view)
}

func (h *hitlHTTPHandler) artifactPreview(w http.ResponseWriter, r *http.Request) {
	service, ok := h.service.(hitlEvidenceService)
	if !ok {
		writeHITLEvidenceError(w, hitl.ErrEvidenceUnsupported)
		return
	}
	index, err := hitlEvidenceIndex(r)
	if err != nil {
		writeHITLEvidenceError(w, err)
		return
	}
	preview, err := service.PreviewArtifactEvidence(r.Context(), r.PathValue("itemID"), index)
	if err != nil {
		writeHITLEvidenceError(w, err)
		return
	}
	writeHITLJSON(w, http.StatusOK, preview)
}

func hitlEvidenceIndex(r *http.Request) (int, error) {
	index, err := strconv.Atoi(r.PathValue("evidenceIndex"))
	if err != nil || index < 0 || index >= 24 {
		return 0, hitl.ErrEvidenceMissing
	}
	return index, nil
}

func writeHITLEvidenceError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "evidence_unavailable"
	message := "The durable evidence could not be loaded."
	switch {
	case errors.Is(err, hitl.ErrEvidenceMissing):
		status, code, message = http.StatusNotFound, "evidence_missing", "This evidence entry no longer exists. Refresh the item before trying again."
	case errors.Is(err, hitl.ErrEvidenceExpired):
		status, code, message = http.StatusGone, "evidence_expired", "The authority reports that this evidence preview has expired. Its durable metadata remains available."
	case errors.Is(err, hitl.ErrEvidenceUnauthorized):
		status, code, message = http.StatusForbidden, "evidence_unauthorized", "This Tangent host has no authority to open the referenced content."
	case errors.Is(err, hitl.ErrEvidenceUnsupported):
		status, code, message = http.StatusUnprocessableEntity, "evidence_unsupported", "No explicitly authorized host adapter can preview this evidence. Its durable metadata remains available."
	case errors.Is(err, hitl.ErrEvidenceTooLarge):
		status, code, message = http.StatusRequestEntityTooLarge, "evidence_too_large", "The adapter preview exceeded Tangent's safe display limit. Open it through its owning system instead."
	case errors.Is(err, hitl.ErrInvalidRequest):
		status, code, message = http.StatusBadRequest, "invalid_evidence", "The stored evidence is not a valid HITL evidence record."
	}
	writeHITLJSON(w, status, map[string]any{
		"contract_version": hitl.ContractVersion,
		"code":             code,
		"message":          message,
	})
}
