package docs

const ReservedSurfaceCapability = "tangent:docs-inbox-service:v1"

// SurfaceAccessPolicy reserves the operator docs surface from generic MCP operations.
type SurfaceAccessPolicy struct{}

func (SurfaceAccessPolicy) Authorize(surfaceID string, capability string) bool {
	return surfaceID != DefaultSurfaceID || capability == ReservedSurfaceCapability
}
