package artifacts

const ReservedSurfaceCapability = "tangent:artifact-inbox-service:v1"

// SurfaceAccessPolicy reserves the operator artifact surface from generic MCP
// operations, exactly as the docs surface is reserved.
type SurfaceAccessPolicy struct{}

func (SurfaceAccessPolicy) Authorize(surfaceID string, capability string) bool {
	return surfaceID != DefaultSurfaceID || capability == ReservedSurfaceCapability
}

// AuthorizeInboxRead admits the pure read workflow only; it never authorizes
// submit, close, resolution, or another lifecycle command.
func (SurfaceAccessPolicy) AuthorizeInboxRead(string) bool { return true }
