package hitl

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type testArtifactPreviewAdapter struct {
	capability ArtifactPreviewCapability
	preview    ArtifactPreview
	calls      int
}

func (adapter *testArtifactPreviewAdapter) Capability() ArtifactPreviewCapability {
	return adapter.capability
}

func (adapter *testArtifactPreviewAdapter) Preview(
	_ context.Context,
	_ ArtifactPreviewRequest,
) (ArtifactPreview, error) {
	adapter.calls++
	return adapter.preview, nil
}

func TestTangentEvidenceUsesDurableReadOnlyProjectionAcrossRestart(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "evidence-restart.db")
	service, _, database, _ := newTestServiceAtPath(t, databasePath)
	first, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: hitlRequest("codex", "worker-1", "evidence-source", "Evidence source"),
		Caller:  directTestCaller("codex", "worker-1"),
	})
	if err != nil {
		t.Fatalf("enqueue source: %v", err)
	}
	reference := evidenceRequest(t, "reference-item", map[string]any{
		"type": "tangent_reference", "label": "Earlier review",
		"surface_id": DefaultSurfaceID, "interaction_id": first.ItemID,
		"revision": first.Revision, "description": "Pinned review context",
	})
	second, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: reference, Caller: directTestCaller("codex", "worker-2"),
	})
	if err != nil {
		t.Fatalf("enqueue reference: %v", err)
	}
	before, err := service.Inbox(context.Background())
	if err != nil {
		t.Fatalf("Inbox before inspect: %v", err)
	}
	view, err := service.InspectTangentEvidence(context.Background(), second.ItemID, 0)
	if err != nil {
		t.Fatalf("InspectTangentEvidence: %v", err)
	}
	if view.Status != "available" || view.Interaction == nil ||
		view.Interaction.InteractionID != first.ItemID || view.ReadOnlyURL == "" {
		t.Fatalf("reference view = %#v", view)
	}
	after, err := service.Inbox(context.Background())
	if err != nil {
		t.Fatalf("Inbox after inspect: %v", err)
	}
	if after.Revision != before.Revision || after.Pending[0].QueueSequence != before.Pending[0].QueueSequence {
		t.Fatalf("evidence inspection changed inbox: before=%#v after=%#v", before, after)
	}
	if closeErr := database.Close(); closeErr != nil {
		t.Fatalf("close original database: %v", closeErr)
	}

	restarted, _, _, _ := newTestServiceAtPath(t, databasePath)
	restartedView, err := restarted.InspectTangentEvidence(context.Background(), second.ItemID, 0)
	if err != nil || restartedView.Interaction == nil || restartedView.Interaction.InteractionID != first.ItemID {
		t.Fatalf("restart reference view = %#v, %v", restartedView, err)
	}
}

func TestTangentEvidenceRejectsAmbientPathAndURIAuthority(t *testing.T) {
	service, _, _, _ := newTestService(t, "evidence-authority.db")
	request := evidenceRequest(t, "unsafe-reference", map[string]any{
		"type": "tangent_reference", "label": "Unsafe",
		"surface_id": "file:///tmp/private-key", "interaction_id": "action:export-all",
	})
	handle, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: request, Caller: directTestCaller("codex", "security-worker"),
	})
	if err != nil {
		t.Fatalf("enqueue schema-valid unsafe reference: %v", err)
	}
	_, err = service.InspectTangentEvidence(context.Background(), handle.ItemID, 0)
	if !errors.Is(err, ErrEvidenceUnauthorized) {
		t.Fatalf("unsafe reference error = %v", err)
	}
}

func TestArtifactPreviewRequiresExactHostCapabilityAndBoundsOutput(t *testing.T) {
	service, _, _, _ := newTestService(t, "artifact-preview.db")
	request := evidenceRequest(t, "artifact-preview", map[string]any{
		"type": "artifact_ref", "label": "Test report", "authority": "torque",
		"artifact_id": "artifact-42", "digest": "sha256:abc",
		"media_type": "text/plain", "retrieval_capability_id": "safe-text-v1",
	})
	handle, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: request, Caller: directTestCaller("codex", "preview-worker"),
	})
	if err != nil {
		t.Fatalf("enqueue artifact: %v", err)
	}
	_, unavailableErr := service.PreviewArtifactEvidence(context.Background(), handle.ItemID, 0)
	if !errors.Is(unavailableErr, ErrEvidenceUnsupported) {
		t.Fatalf("preview without adapter error = %v", unavailableErr)
	}
	adapter := &testArtifactPreviewAdapter{
		capability: ArtifactPreviewCapability{Authority: "torque", CapabilityID: "safe-text-v1"},
		preview:    ArtifactPreview{Kind: "text", MediaType: "text/plain", Content: "42 checks passed"},
	}
	if registerErr := service.RegisterArtifactPreviewAdapter(adapter); registerErr != nil {
		t.Fatalf("RegisterArtifactPreviewAdapter: %v", registerErr)
	}
	preview, err := service.PreviewArtifactEvidence(context.Background(), handle.ItemID, 0)
	if err != nil || preview.Status != "available" || preview.Content != "42 checks passed" || adapter.calls != 1 {
		t.Fatalf("preview = %#v, calls=%d, err=%v", preview, adapter.calls, err)
	}
	adapter.preview = ArtifactPreview{Kind: "text", Content: strings.Repeat("x", maximumArtifactPreviewBytes+1)}
	_, oversizedErr := service.PreviewArtifactEvidence(context.Background(), handle.ItemID, 0)
	if !errors.Is(oversizedErr, ErrEvidenceTooLarge) {
		t.Fatalf("oversized preview error = %v", oversizedErr)
	}
	adapter.preview = ArtifactPreview{Kind: "external_link", ExternalURL: "file:///tmp/report.pdf"}
	_, unsafeURLErr := service.PreviewArtifactEvidence(context.Background(), handle.ItemID, 0)
	if !errors.Is(unsafeURLErr, ErrEvidenceUnauthorized) {
		t.Fatalf("unsafe external URL error = %v", unsafeURLErr)
	}
}

func TestArtifactPreviewReportsExpiredAndUnsafeReferencesWithoutCallingAdapter(t *testing.T) {
	service, _, _, _ := newTestService(t, "artifact-expiry.db")
	service.now = func() time.Time { return time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC) }
	adapter := &testArtifactPreviewAdapter{
		capability: ArtifactPreviewCapability{Authority: "torque", CapabilityID: "safe-text-v1"},
		preview:    ArtifactPreview{Kind: "text", Content: "must not be read"},
	}
	if registerErr := service.RegisterArtifactPreviewAdapter(adapter); registerErr != nil {
		t.Fatalf("RegisterArtifactPreviewAdapter: %v", registerErr)
	}
	expired := evidenceRequest(t, "artifact-expired", map[string]any{
		"type": "artifact_ref", "label": "Expired report", "authority": "torque",
		"artifact_id": "artifact-43", "digest": "sha256:def",
		"retrieval_capability_id": "safe-text-v1", "expires_at": "2026-09-04T11:59:59Z",
	})
	handle, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: expired, Caller: directTestCaller("codex", "expiry-worker"),
	})
	if err != nil {
		t.Fatalf("enqueue expired artifact: %v", err)
	}
	_, expiredErr := service.PreviewArtifactEvidence(context.Background(), handle.ItemID, 0)
	if !errors.Is(expiredErr, ErrEvidenceExpired) {
		t.Fatalf("expired preview error = %v", expiredErr)
	}
	if adapter.calls != 0 {
		t.Fatalf("expired reference reached adapter %d times", adapter.calls)
	}

	unsafe := evidenceRequest(t, "artifact-unsafe", map[string]any{
		"type": "artifact_ref", "label": "Unsafe authority", "authority": "file:",
		"artifact_id": "artifact-44", "digest": "sha256:ghi",
		"retrieval_capability_id": "safe-text-v1",
	})
	unsafeHandle, err := service.Enqueue(context.Background(), EnqueueInput{
		Request: unsafe, Caller: directTestCaller("codex", "unsafe-worker"),
	})
	if err != nil {
		t.Fatalf("enqueue unsafe artifact: %v", err)
	}
	_, unsafeReferenceErr := service.PreviewArtifactEvidence(context.Background(), unsafeHandle.ItemID, 0)
	if !errors.Is(unsafeReferenceErr, ErrEvidenceUnauthorized) {
		t.Fatalf("unsafe preview error = %v", unsafeReferenceErr)
	}
	if adapter.calls != 0 {
		t.Fatalf("unsafe reference reached adapter %d times", adapter.calls)
	}
}

func evidenceRequest(t *testing.T, key string, evidence map[string]any) json.RawMessage {
	t.Helper()
	request := map[string]any{
		"contract_version": ContractVersion,
		"kind":             "approval",
		"idempotency_key":  key,
		"title":            "Review evidence",
		"summary":          "Evidence must stay durable and read-only.",
		"request":          "Inspect the evidence before deciding.",
		"source": map[string]any{
			"application_id": "codex", "agent_id": "evidence-worker",
		},
		"evidence": []any{evidence},
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal evidence request: %v", err)
	}
	return raw
}
