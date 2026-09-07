package launchagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ExecLaunchctl drives the real launchctl binary. It is the only place this
// package runs a subprocess, and nothing in the repository's tests constructs
// it: tests use a fake so no test can ever touch a real launchd domain.
type ExecLaunchctl struct {
	// Path overrides the launchctl executable; empty means "launchctl" on PATH.
	Path string
}

func (e ExecLaunchctl) command(ctx context.Context, args ...string) *exec.Cmd {
	path := e.Path
	if path == "" {
		path = "launchctl"
	}
	// #nosec G204 -- args are a fixed verb plus a domain/path this package built.
	return exec.CommandContext(ctx, path, args...)
}

// Bootstrap runs `launchctl bootstrap <domain> <plist>`.
func (e ExecLaunchctl) Bootstrap(ctx context.Context, domain, plistPath string) error {
	return run(e.command(ctx, "bootstrap", domain, plistPath))
}

// Bootout runs `launchctl bootout <service-target>`. launchctl reports a
// service that is not loaded with exit code 3 and "No such process"; that is
// mapped to ErrNotLoaded so a first install and a repeated uninstall read as
// already done.
func (e ExecLaunchctl) Bootout(ctx context.Context, serviceTarget string) error {
	err := run(e.command(ctx, "bootout", serviceTarget))
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && (exitErr.ExitCode() == 3 || strings.Contains(err.Error(), "No such process")) {
		return ErrNotLoaded
	}
	return err
}

// Loaded runs `launchctl print <service-target>`, which exits 0 only when the
// service is loaded in that domain.
func (e ExecLaunchctl) Loaded(ctx context.Context, serviceTarget string) (bool, error) {
	err := run(e.command(ctx, "print", serviceTarget))
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, err
}

func run(cmd *exec.Cmd) error {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return fmt.Errorf("%w: %s", err, detail)
		}
		return err
	}
	return nil
}
