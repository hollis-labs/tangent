package room

import (
	"errors"
	"testing"
)

func TestNormalizeWhiteboardAssetRefRejectsBrowserOnlyURI(t *testing.T) {
	_, err := normalizeWhiteboardAssetRef(WhiteboardAssetRef{
		AssetID: "asset-inline-uri",
		URI:     "blob:local-image",
	})
	if !errors.Is(err, ErrInvalidWhiteboardAssetRef) {
		t.Fatalf("normalizeWhiteboardAssetRef err = %v, want ErrInvalidWhiteboardAssetRef", err)
	}
}

func TestNormalizeWhiteboardExportRefRejectsBrowserOnlyURI(t *testing.T) {
	_, err := normalizeWhiteboardExportRef(WhiteboardExportRef{
		Name: "board-inline-export.png",
		Kind: "png",
		URI:  "data:image/png;base64,AAA",
	})
	if !errors.Is(err, ErrInvalidWhiteboardExportRef) {
		t.Fatalf("normalizeWhiteboardExportRef err = %v, want ErrInvalidWhiteboardExportRef", err)
	}
}

func TestValidateWhiteboardArtifactURIRejectsNonArtifactScheme(t *testing.T) {
	err := validateWhiteboardArtifactURI("https://assets.example.test/reference.png")
	if !errors.Is(err, ErrInvalidWhiteboardAssetRef) {
		t.Fatalf("validateWhiteboardArtifactURI err = %v, want ErrInvalidWhiteboardAssetRef", err)
	}
}

func TestValidateWhiteboardExportURIRejectsNonArtifactScheme(t *testing.T) {
	err := validateWhiteboardExportURI("https://assets.example.test/export.png")
	if !errors.Is(err, ErrInvalidWhiteboardExportRef) {
		t.Fatalf("validateWhiteboardExportURI err = %v, want ErrInvalidWhiteboardExportRef", err)
	}
}
