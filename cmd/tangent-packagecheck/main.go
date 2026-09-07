// Command tangent-packagecheck verifies that a built macOS .app bundle is
// well-formed: a valid Info.plist with the expected bundle id, a present
// and executable main binary, and a present icon. scripts/build-macos-app.sh
// runs it against the staged bundle before promoting it into place, so a
// malformed bundle fails `make build-app` instead of surfacing as a later
// runtime surprise (double-clicking an app with a bad Info.plist, or one
// missing its icon).
//
// Usage:
//
//	tangent-packagecheck [path/to/Bundle.app]
//
// The bundle path defaults to "Tangent.app" in the current directory.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/hollis-labs/tangent/internal/packagecheck"
)

func main() {
	appPath := "Tangent.app"
	if len(os.Args) > 1 {
		appPath = os.Args[1]
	}
	if err := packagecheck.VerifyMacOSBundle(context.Background(), appPath); err != nil {
		fmt.Fprintf(os.Stderr, "tangent-packagecheck: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("verified: %s is a well-formed macOS application bundle (bundle id %s)\n", appPath, packagecheck.ExpectedBundleID)
}
