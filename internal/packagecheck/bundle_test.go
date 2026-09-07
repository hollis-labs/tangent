package packagecheck

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// plutil and iconutil are macOS-only. The "go" CI job (ubuntu-latest) still
// compiles this package — it has no cgo or Wails dependency and cross-
// compiles fine — but these tests must not run there.
func skipUnlessDarwin(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("packagecheck shells out to plutil, which is macOS-only")
	}
}

const validPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleExecutable</key>
	<string>tangent-app</string>
	<key>CFBundleIdentifier</key>
	<string>com.hollislabs.tangent</string>
	<key>CFBundleIconFile</key>
	<string>AppIcon</string>
	<key>CFBundleShortVersionString</key>
	<string>0.1.0</string>
	<key>CFBundleVersion</key>
	<string>0.1.0</string>
</dict>
</plist>
`

// buildBundle lays out a well-formed bundle under t.TempDir() and returns
// its path. Callers mutate the result to construct failure cases.
func buildBundle(t *testing.T) string {
	t.Helper()
	appPath := filepath.Join(t.TempDir(), "Tangent.app")
	mustMkdir(t, filepath.Join(appPath, "Contents", "MacOS"))
	mustMkdir(t, filepath.Join(appPath, "Contents", "Resources"))
	mustWrite(t, filepath.Join(appPath, "Contents", "Info.plist"), validPlist, 0o644)
	mustWrite(t, filepath.Join(appPath, "Contents", "MacOS", "tangent-app"), "#!/bin/sh\nexit 0\n", 0o755)
	mustWrite(t, filepath.Join(appPath, "Contents", "Resources", "AppIcon.icns"), "icns-bytes-stand-in", 0o644)
	return appPath
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
}

func mustWrite(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func TestVerifyMacOSBundleAcceptsWellFormedBundle(t *testing.T) {
	skipUnlessDarwin(t)
	appPath := buildBundle(t)
	if err := VerifyMacOSBundle(context.Background(), appPath); err != nil {
		t.Fatalf("VerifyMacOSBundle: %v", err)
	}
}

func TestVerifyMacOSBundleRejectsMissingBundle(t *testing.T) {
	skipUnlessDarwin(t)
	err := VerifyMacOSBundle(context.Background(), filepath.Join(t.TempDir(), "Nonexistent.app"))
	if err == nil || !strings.Contains(err.Error(), "stat bundle") {
		t.Fatalf("VerifyMacOSBundle error = %v, want stat bundle error", err)
	}
}

func TestVerifyMacOSBundleRejectsInvalidPlist(t *testing.T) {
	skipUnlessDarwin(t)
	appPath := buildBundle(t)
	mustWrite(t, filepath.Join(appPath, "Contents", "Info.plist"), "not a plist", 0o644)
	err := VerifyMacOSBundle(context.Background(), appPath)
	if err == nil || !strings.Contains(err.Error(), "plutil -lint") {
		t.Fatalf("VerifyMacOSBundle error = %v, want plutil -lint failure", err)
	}
}

func TestVerifyMacOSBundleRejectsWrongBundleID(t *testing.T) {
	skipUnlessDarwin(t)
	appPath := buildBundle(t)
	rewritten := strings.Replace(validPlist, "com.hollislabs.tangent", "com.example.wrong", 1)
	mustWrite(t, filepath.Join(appPath, "Contents", "Info.plist"), rewritten, 0o644)
	err := VerifyMacOSBundle(context.Background(), appPath)
	if err == nil || !strings.Contains(err.Error(), "CFBundleIdentifier") {
		t.Fatalf("VerifyMacOSBundle error = %v, want CFBundleIdentifier mismatch", err)
	}
}

func TestVerifyMacOSBundleRejectsUnsubstitutedVersionPlaceholder(t *testing.T) {
	skipUnlessDarwin(t)
	appPath := buildBundle(t)
	rewritten := strings.ReplaceAll(validPlist, "0.1.0", "__TANGENT_VERSION__")
	mustWrite(t, filepath.Join(appPath, "Contents", "Info.plist"), rewritten, 0o644)
	err := VerifyMacOSBundle(context.Background(), appPath)
	if err == nil || !strings.Contains(err.Error(), "unsubstituted template placeholder") {
		t.Fatalf("VerifyMacOSBundle error = %v, want unsubstituted placeholder failure", err)
	}
}

func TestVerifyMacOSBundleRejectsMissingExecutable(t *testing.T) {
	skipUnlessDarwin(t)
	appPath := buildBundle(t)
	if err := os.Remove(filepath.Join(appPath, "Contents", "MacOS", "tangent-app")); err != nil {
		t.Fatal(err)
	}
	err := VerifyMacOSBundle(context.Background(), appPath)
	if err == nil || !strings.Contains(err.Error(), "CFBundleExecutable") {
		t.Fatalf("VerifyMacOSBundle error = %v, want CFBundleExecutable failure", err)
	}
}

func TestVerifyMacOSBundleRejectsNonExecutableBinary(t *testing.T) {
	skipUnlessDarwin(t)
	appPath := buildBundle(t)
	execPath := filepath.Join(appPath, "Contents", "MacOS", "tangent-app")
	if err := os.Chmod(execPath, 0o600); err != nil {
		t.Fatal(err)
	}
	err := VerifyMacOSBundle(context.Background(), appPath)
	if err == nil || !strings.Contains(err.Error(), "is not executable") {
		t.Fatalf("VerifyMacOSBundle error = %v, want not-executable failure", err)
	}
}

func TestVerifyMacOSBundleRejectsMissingIcon(t *testing.T) {
	skipUnlessDarwin(t)
	appPath := buildBundle(t)
	if err := os.Remove(filepath.Join(appPath, "Contents", "Resources", "AppIcon.icns")); err != nil {
		t.Fatal(err)
	}
	err := VerifyMacOSBundle(context.Background(), appPath)
	if err == nil || !strings.Contains(err.Error(), "CFBundleIconFile") {
		t.Fatalf("VerifyMacOSBundle error = %v, want CFBundleIconFile failure", err)
	}
}

func TestVerifyMacOSBundleReportsEveryProblemTogether(t *testing.T) {
	skipUnlessDarwin(t)
	appPath := buildBundle(t)
	rewritten := strings.Replace(validPlist, "com.hollislabs.tangent", "com.example.wrong", 1)
	mustWrite(t, filepath.Join(appPath, "Contents", "Info.plist"), rewritten, 0o644)
	if err := os.Remove(filepath.Join(appPath, "Contents", "Resources", "AppIcon.icns")); err != nil {
		t.Fatal(err)
	}
	err := VerifyMacOSBundle(context.Background(), appPath)
	if err == nil {
		t.Fatal("VerifyMacOSBundle: want error, got nil")
	}
	if !strings.Contains(err.Error(), "CFBundleIdentifier") || !strings.Contains(err.Error(), "CFBundleIconFile") {
		t.Fatalf("VerifyMacOSBundle error = %v, want both CFBundleIdentifier and CFBundleIconFile failures joined", err)
	}
}
