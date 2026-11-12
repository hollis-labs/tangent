package filemigrate

import (
	"errors"
	"strings"
	"testing"
)

// When the second tree cannot be installed after the first was, the error says which is in place
// and which staging directory is left, because a rerun refuses on the installed one.
func TestInstallFailureNamesWhatIsInstalledAndWhatIsLeft(t *testing.T) {
	err := installFailure("/data", []string{"artifacts", "plugins"}, []string{"/data/artifacts.migrating", "/data/plugins.migrating"}, 1, []string{"artifacts"}, errors.New("rename: denied"))
	for _, want := range []string{"installing plugins failed", "rename: denied", "Already installed under /data: artifacts", "/data/plugins.migrating"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q missing from: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "/data/artifacts.migrating") {
		t.Errorf("an installed tree's staging directory is listed as left: %v", err)
	}
}
