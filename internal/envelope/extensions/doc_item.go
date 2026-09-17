package extensions

import (
	"bytes"

	"github.com/hollis-labs/tangent/internal/envelope"
)

// DocsPackageID is the package tangent.doc-item ships in.
const DocsPackageID = "tangent.docs"

// DocItemEnvelopeType is the immutable definition kind used by the Docs
// inbox (CW-20260917-0009).
const DocItemEnvelopeType = "tangent.doc-item"

// DocItemContractVersion is the payload contract version.
const DocItemContractVersion = "1.0"

// DocItemDefinitionVersion is the version of the shipped manifest.
const DocItemDefinitionVersion = "1.0"

var docItemSchema = mustPackageFile(DocsPackageID, DocItemEnvelopeType, requestSchemaFileName)

// DocItemContractSchema returns a copy of the doc-item request schema bundle.
func DocItemContractSchema() []byte {
	return bytes.Clone(docItemSchema)
}

// RegisterDocItem registers the doc-item request definition on svc.
func RegisterDocItem(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, DocsPackageID, DocItemEnvelopeType)
}
