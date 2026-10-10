package docs

const ReservedSurfaceCapability = "tangent:docs-inbox-service:v1"

// SurfaceAccessPolicy reserves the operator docs surface from generic MCP operations.
type SurfaceAccessPolicy struct{}

func (SurfaceAccessPolicy) Authorize(surfaceID string, capability string) bool {
	return surfaceID != DefaultSurfaceID || capability == ReservedSurfaceCapability
}

// AuthorizeInboxRead admits the pure read workflow only; it never authorizes
// submit, close, resolution, or another lifecycle command. Caller View checks
// remain mandatory in the interaction service.
func (SurfaceAccessPolicy) AuthorizeInboxRead(string) bool { return true }
