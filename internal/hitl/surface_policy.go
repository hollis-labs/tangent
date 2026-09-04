package hitl

const reservedSurfaceCapability = "tangent:hitl-inbox-service:v1"

const evidenceReferenceCapability = "tangent:hitl-evidence-reference:v1"

// SurfaceAccessPolicy reserves the operator HITL surface from generic MCP
// open/submit/close operations. The capability is application-internal and is
// never accepted from tool arguments.
type SurfaceAccessPolicy struct{}

func (SurfaceAccessPolicy) Authorize(surfaceID string, capability string) bool {
	return surfaceID != DefaultSurfaceID ||
		capability == reservedSurfaceCapability ||
		capability == evidenceReferenceCapability
}
