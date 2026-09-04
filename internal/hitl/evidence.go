package hitl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/tangent/internal/interaction"
)

const (
	maximumReferenceRequestBytes = 128 * 1024
	maximumArtifactPreviewBytes  = 64 * 1024
)

var (
	ErrEvidenceMissing      = errors.New("hitl evidence: reference is missing")
	ErrEvidenceExpired      = errors.New("hitl evidence: reference has expired")
	ErrEvidenceUnauthorized = errors.New("hitl evidence: reference is unauthorized")
	ErrEvidenceUnsupported  = errors.New("hitl evidence: preview is unsupported")
	ErrEvidenceTooLarge     = errors.New("hitl evidence: preview exceeds the size limit")

	evidenceIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,511}$`)
	evidenceAuthority  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,127}$`)

	previewRegistryMu sync.RWMutex
	previewRegistry   = map[*Service]map[string]ArtifactPreviewAdapter{}
)

type storedEvidenceRequest struct {
	Evidence []json.RawMessage `json:"evidence"`
}

type evidenceDiscriminator struct {
	Type string `json:"type"`
}

type TangentReferenceEvidence struct {
	Type          string `json:"type"`
	Label         string `json:"label"`
	SurfaceID     string `json:"surface_id"`
	InteractionID string `json:"interaction_id,omitempty"`
	Revision      *int64 `json:"revision,omitempty"`
	Description   string `json:"description,omitempty"`
}

type TangentSurfaceEvidenceView struct {
	SurfaceID string                   `json:"surface_id"`
	State     interaction.SurfaceState `json:"state"`
	Revision  int64                    `json:"revision"`
	UpdatedAt time.Time                `json:"updated_at"`
}

type TangentInteractionEvidenceView struct {
	InteractionID     string                       `json:"interaction_id"`
	State             interaction.InteractionState `json:"state"`
	Revision          int64                        `json:"revision"`
	DefinitionKind    string                       `json:"definition_kind"`
	DefinitionVersion string                       `json:"definition_version"`
	RequestSnapshot   json.RawMessage              `json:"request_snapshot,omitempty"`
	RequestOmitted    bool                         `json:"request_omitted,omitempty"`
	UpdatedAt         time.Time                    `json:"updated_at"`
}

type TangentReferenceEvidenceView struct {
	Status            string                          `json:"status"`
	Label             string                          `json:"label"`
	Description       string                          `json:"description,omitempty"`
	RequestedRevision *int64                          `json:"requested_revision,omitempty"`
	Surface           TangentSurfaceEvidenceView      `json:"surface"`
	Interaction       *TangentInteractionEvidenceView `json:"interaction,omitempty"`
	ReadOnlyURL       string                          `json:"read_only_url,omitempty"`
}

type ArtifactRefEvidence struct {
	Type                  string `json:"type"`
	Label                 string `json:"label"`
	Authority             string `json:"authority"`
	ArtifactID            string `json:"artifact_id"`
	Revision              string `json:"revision,omitempty"`
	Digest                string `json:"digest,omitempty"`
	MediaType             string `json:"media_type,omitempty"`
	LogicalKind           string `json:"logical_kind,omitempty"`
	SizeBytes             *int64 `json:"size_bytes,omitempty"`
	Sensitivity           string `json:"sensitivity,omitempty"`
	RetrievalCapabilityID string `json:"retrieval_capability_id,omitempty"`
	ExpiresAt             string `json:"expires_at,omitempty"`
	RetentionPolicy       string `json:"retention_policy,omitempty"`
	SafePreviewArtifactID string `json:"safe_preview_artifact_id,omitempty"`
}

type ArtifactPreviewCapability struct {
	Authority    string
	CapabilityID string
}

type ArtifactPreviewRequest struct {
	ArtifactRefEvidence
	ItemID        string
	EvidenceIndex int
}

// ArtifactPreview is deliberately text-or-link only. Binary PDF/image viewing
// remains the responsibility of an explicitly registered host adapter.
type ArtifactPreview struct {
	Status      string `json:"status"`
	Kind        string `json:"kind"`
	MediaType   string `json:"media_type,omitempty"`
	Content     string `json:"content,omitempty"`
	ExternalURL string `json:"external_url,omitempty"`
	Notice      string `json:"notice,omitempty"`
}

// ArtifactPreviewAdapter is host-established capability, not caller data.
// Tangent never treats authority, artifact IDs, paths, URIs, or action IDs in
// an evidence payload as permission to read or execute anything.
type ArtifactPreviewAdapter interface {
	Capability() ArtifactPreviewCapability
	Preview(context.Context, ArtifactPreviewRequest) (ArtifactPreview, error)
}

// RegisterArtifactPreviewAdapter makes one exact authority/capability pair
// available to this service instance. Duplicate pairs are rejected.
func (s *Service) RegisterArtifactPreviewAdapter(adapter ArtifactPreviewAdapter) error {
	if s == nil || adapter == nil {
		return fmt.Errorf("%w: preview adapter is required", ErrInvalidRequest)
	}
	capability := adapter.Capability()
	if !validAuthority(capability.Authority) || !validEvidenceIdentifier(capability.CapabilityID) {
		return fmt.Errorf("%w: preview adapter authority and capability must be safe identifiers", ErrInvalidRequest)
	}
	key := artifactPreviewKey(capability.Authority, capability.CapabilityID)
	previewRegistryMu.Lock()
	defer previewRegistryMu.Unlock()
	adapters := previewRegistry[s]
	if adapters == nil {
		adapters = map[string]ArtifactPreviewAdapter{}
		previewRegistry[s] = adapters
	}
	if _, exists := adapters[key]; exists {
		return fmt.Errorf("%w: preview adapter already registered", ErrInvalidRequest)
	}
	adapters[key] = adapter
	return nil
}

// InspectTangentEvidence resolves only the stored evidence entry at index. It
// never accepts a caller-provided surface or interaction identifier directly.
func (s *Service) InspectTangentEvidence(
	ctx context.Context,
	itemID string,
	evidenceIndex int,
) (TangentReferenceEvidenceView, error) {
	raw, err := s.storedEvidence(ctx, itemID, evidenceIndex, "tangent_reference")
	if err != nil {
		return TangentReferenceEvidenceView{}, err
	}
	var reference TangentReferenceEvidence
	if decodeErr := json.Unmarshal(raw, &reference); decodeErr != nil {
		return TangentReferenceEvidenceView{}, fmt.Errorf("%w: decode Tangent reference: %w", ErrInvalidRequest, decodeErr)
	}
	if !validEvidenceIdentifier(reference.SurfaceID) ||
		(reference.InteractionID != "" && !validEvidenceIdentifier(reference.InteractionID)) {
		return TangentReferenceEvidenceView{}, fmt.Errorf("%w: Tangent identifiers cannot be paths or URIs", ErrEvidenceUnauthorized)
	}
	snapshot, err := s.interactions.InspectSurfaceReference(
		ctx, reference.SurfaceID, reference.InteractionID, evidenceReferenceCapability,
	)
	if errors.Is(err, interaction.ErrUnauthorized) {
		return TangentReferenceEvidenceView{}, ErrEvidenceUnauthorized
	}
	if errors.Is(err, interaction.ErrNotFound) {
		return TangentReferenceEvidenceView{}, ErrEvidenceMissing
	}
	if err != nil {
		return TangentReferenceEvidenceView{}, err
	}
	status := "available"
	if snapshot.Surface.State == interaction.SurfaceStateExpired {
		status = "expired"
	}
	view := TangentReferenceEvidenceView{
		Status: status, Label: reference.Label, Description: reference.Description,
		RequestedRevision: reference.Revision,
		Surface: TangentSurfaceEvidenceView{
			SurfaceID: snapshot.Surface.ID, State: snapshot.Surface.State,
			Revision: snapshot.Surface.Revision, UpdatedAt: snapshot.Surface.UpdatedAt,
		},
	}
	if snapshot.Interaction != nil {
		record := snapshot.Interaction
		if reference.Revision != nil && *reference.Revision != record.Revision {
			view.Status = "revision_mismatch"
		}
		interactionView := &TangentInteractionEvidenceView{
			InteractionID: record.ID, State: record.State, Revision: record.Revision,
			DefinitionKind: record.Definition.Kind, DefinitionVersion: record.Definition.Version,
			UpdatedAt: record.UpdatedAt,
		}
		if len(record.RequestSnapshot) <= maximumReferenceRequestBytes {
			interactionView.RequestSnapshot = append(json.RawMessage(nil), record.RequestSnapshot...)
		} else {
			interactionView.RequestOmitted = true
		}
		view.Interaction = interactionView
		if record.SurfaceID == DefaultSurfaceID && isHITLRecord(*record) {
			view.ReadOnlyURL = InboxURL + "/items/" + url.PathEscape(record.ID)
		}
	}
	return view, nil
}

// PreviewArtifactEvidence invokes only an exact host-registered adapter for the
// exact capability named by the immutable stored evidence entry.
func (s *Service) PreviewArtifactEvidence(
	ctx context.Context,
	itemID string,
	evidenceIndex int,
) (ArtifactPreview, error) {
	raw, err := s.storedEvidence(ctx, itemID, evidenceIndex, "artifact_ref")
	if err != nil {
		return ArtifactPreview{}, err
	}
	var reference ArtifactRefEvidence
	if decodeErr := json.Unmarshal(raw, &reference); decodeErr != nil {
		return ArtifactPreview{}, fmt.Errorf("%w: decode artifact reference: %w", ErrInvalidRequest, decodeErr)
	}
	if !validAuthority(reference.Authority) || !validEvidenceIdentifier(reference.ArtifactID) ||
		(reference.SafePreviewArtifactID != "" && !validEvidenceIdentifier(reference.SafePreviewArtifactID)) ||
		(reference.RetrievalCapabilityID != "" && !validEvidenceIdentifier(reference.RetrievalCapabilityID)) {
		return ArtifactPreview{}, fmt.Errorf("%w: artifact fields cannot be paths, URIs, or action identifiers", ErrEvidenceUnauthorized)
	}
	if reference.ExpiresAt != "" {
		expiresAt, parseErr := time.Parse(time.RFC3339, reference.ExpiresAt)
		if parseErr != nil {
			return ArtifactPreview{}, fmt.Errorf("%w: invalid artifact expiry", ErrInvalidRequest)
		}
		if !s.now().Before(expiresAt) {
			return ArtifactPreview{}, ErrEvidenceExpired
		}
	}
	if reference.RetrievalCapabilityID == "" {
		return ArtifactPreview{}, fmt.Errorf("%w: no retrieval capability was declared", ErrEvidenceUnsupported)
	}
	previewRegistryMu.RLock()
	adapter := previewRegistry[s][artifactPreviewKey(reference.Authority, reference.RetrievalCapabilityID)]
	previewRegistryMu.RUnlock()
	if adapter == nil {
		return ArtifactPreview{}, fmt.Errorf("%w: no host adapter supports %s/%s", ErrEvidenceUnsupported, reference.Authority, reference.RetrievalCapabilityID)
	}
	preview, err := adapter.Preview(ctx, ArtifactPreviewRequest{
		ArtifactRefEvidence: reference, ItemID: itemID, EvidenceIndex: evidenceIndex,
	})
	if err != nil {
		return ArtifactPreview{}, err
	}
	if preview.Kind != "text" && preview.Kind != "external_link" {
		return ArtifactPreview{}, fmt.Errorf("%w: adapter returned preview kind %q", ErrEvidenceUnsupported, preview.Kind)
	}
	if len(preview.Content) > maximumArtifactPreviewBytes {
		return ArtifactPreview{}, ErrEvidenceTooLarge
	}
	if preview.Kind == "external_link" {
		parsed, parseErr := url.Parse(preview.ExternalURL)
		if parseErr != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return ArtifactPreview{}, fmt.Errorf("%w: adapter external link must be an absolute HTTPS URL", ErrEvidenceUnauthorized)
		}
	}
	preview.Status = "available"
	return preview, nil
}

func (s *Service) storedEvidence(
	ctx context.Context,
	itemID string,
	evidenceIndex int,
	expectedType string,
) (json.RawMessage, error) {
	if evidenceIndex < 0 || evidenceIndex >= 24 {
		return nil, ErrEvidenceMissing
	}
	item, err := s.InspectOperatorItem(ctx, itemID)
	if errors.Is(err, interaction.ErrUnauthorized) {
		return nil, ErrEvidenceUnauthorized
	}
	if errors.Is(err, interaction.ErrNotFound) {
		return nil, ErrEvidenceMissing
	}
	if err != nil {
		return nil, err
	}
	var request storedEvidenceRequest
	if decodeErr := json.Unmarshal(item.RequestSnapshot, &request); decodeErr != nil {
		return nil, fmt.Errorf("%w: decode stored HITL evidence: %w", ErrInvalidRequest, decodeErr)
	}
	if evidenceIndex >= len(request.Evidence) {
		return nil, ErrEvidenceMissing
	}
	var discriminator evidenceDiscriminator
	if decodeErr := json.Unmarshal(request.Evidence[evidenceIndex], &discriminator); decodeErr != nil {
		return nil, fmt.Errorf("%w: decode evidence type: %w", ErrInvalidRequest, decodeErr)
	}
	if discriminator.Type != expectedType {
		return nil, fmt.Errorf("%w: evidence entry is %q, not %q", ErrEvidenceUnsupported, discriminator.Type, expectedType)
	}
	return append(json.RawMessage(nil), request.Evidence[evidenceIndex]...), nil
}

func validEvidenceIdentifier(value string) bool {
	return evidenceIdentifier.MatchString(value) && !strings.Contains(value, "..")
}

func validAuthority(value string) bool {
	return evidenceAuthority.MatchString(value) && !strings.Contains(value, "..")
}

func artifactPreviewKey(authority string, capability string) string {
	return authority + "\x00" + capability
}
