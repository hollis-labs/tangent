package extensions

import (
	"bytes"

	"github.com/hollis-labs/tangent/internal/envelope"
)

// ArtifactsPackageID is the package tangent.artifact-item ships in.
const ArtifactsPackageID = "tangent.artifacts"

// ArtifactItemEnvelopeType is the immutable definition kind used by the
// artifact inbox (ADR 0015, CW-20261024-0061).
const ArtifactItemEnvelopeType = "tangent.artifact-item"

// ArtifactItemContractVersion is the payload contract version.
const ArtifactItemContractVersion = "1.0"

// ArtifactItemDefinitionVersion is the version of the shipped manifest.
const ArtifactItemDefinitionVersion = "1.0"

var artifactItemSchema = mustPackageFile(ArtifactsPackageID, ArtifactItemEnvelopeType, requestSchemaFileName)

// ArtifactItemContractSchema returns a copy of the artifact-item request schema.
func ArtifactItemContractSchema() []byte {
	return bytes.Clone(artifactItemSchema)
}

// RegisterArtifactItem registers the artifact-item request definition on svc.
func RegisterArtifactItem(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, ArtifactsPackageID, ArtifactItemEnvelopeType)
}
