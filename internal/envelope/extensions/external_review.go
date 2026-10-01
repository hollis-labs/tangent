package extensions

import "github.com/hollis-labs/tangent/internal/envelope"

// ExternalReviewEnvelopeType presents an external resource without knowing its application.
const ExternalReviewEnvelopeType = "tangent.external-review"

// RegisterExternalReview installs the domain-free review presentation contract.
func RegisterExternalReview(svc *envelope.Service) error {
	return registerPackagedDefinition(svc, "tangent.review", ExternalReviewEnvelopeType)
}
