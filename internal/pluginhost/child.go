package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/plugin-sdk/subprocess"
)

// One subprocess plugin's process (CW-20260910-0034).
//
// # What this buys, which is the whole point of the task
//
// A compiled-in application plugin puts that application's client code inside
// Tangent's binary. `internal/plugins/torque/torque.go` is the only file in the
// repository that knows Torque exists, and it is linked into the server. A
// subprocess plugin holds its dependency in its own process, so Tangent's
// binary stays domain-free, a plugin crash cannot take the host with it, and a
// plugin versions independently of a Tangent release.
//
// # Termination, and why the careful part is not the guarantee
//
// stop() is the graceful path: `plugin/unload`, then close the child's stdin,
// then wait, then SIGKILL the process group.
//
// It is NOT what makes termination correct, and reading it as though it were is
// the mistake this comment exists to prevent. `subprocess.Serve` ends its loop
// on STDIN EOF and calls the plugin's own Unload on the way out — so when this
// process dies by any route at all, including a panic, a SIGKILL, or the
// os.Exit branch `cmd/tangent` used to take on a shutdown timeout, the kernel
// closes the child's pipes and the child reaps itself. The OS is the backstop;
// stop() is the courtesy of asking first.
//
// That ordering matters for what it licenses. It means no shutdown path has to
// be proven to run for a child to die, so the budget below can be short and the
// failure mode of a wedged child is a SIGKILL rather than a held shutdown.
//
// # Setpgid
//
// A plugin runtime that forks — node, python, a shell wrapper — leaves
// grandchildren that a SIGKILL to the child's pid does not reach. Spawning into
// a fresh process group means the negative-pid kill reaches all of them. Nanite
// found this the hard way; it is copied rather than rediscovered.

const (
	// childHandshakeBudget bounds plugin/init and plugin/load. A plugin that
	// cannot introduce itself this quickly is not one this host will wait on at
	// boot — a slow boot is a boot an operator reads as a hang.
	childHandshakeBudget = 10 * time.Second

	// childUnloadBudget bounds the graceful half of stop(): the plugin/unload
	// call and the wait for exit after stdin closes.
	//
	// Short on purpose, and it is the per-child figure UnloadAll multiplies.
	// The host's own shutdown deadline is ten seconds; N children each waiting
	// a generous budget is how an unbounded sum reproduces a held shutdown with
	// a different name on it. There is nothing to lose by being brisk, because
	// the SIGKILL below is not a failure mode — it is the same outcome the
	// child reaches on its own when its pipes close.
	childUnloadBudget = 2 * time.Second

	// childReapBudget bounds the wait after SIGKILL. A process that is not
	// reaped in this long is one whose grandchild is pinning the pipe, and
	// continuing beats hanging the caller.
	childReapBudget = 2 * time.Second
)

// ChildSpec is what the host needs to spawn one plugin.
type ChildSpec struct {
	// ID is the plugin id, used in logs and errors before the child has
	// introduced itself.
	ID string
	// Command is the executable.
	Command string
	// Args are its arguments.
	Args []string
	// WorkDir is the child's working directory. Empty means this process's.
	WorkDir string
	// Env is added to the child's environment.
	//
	// THIS IS HOW A SUBPROCESS PLUGIN GETS ITS SECRETS, and it is deliberate:
	// see the Config field on the init params in start(). A plugin reads its
	// own environment exactly as the compiled-in ones do today.
	Env []string
	// DataDir and CacheDir are the per-plugin roots the SDK's InitParams
	// carries. A plugin treats a missing DataDir as fatal for persistence,
	// so the host supplies one rather than letting the plugin guess.
	DataDir  string
	CacheDir string
}

// child is one running subprocess plugin.
type child struct {
	spec ChildSpec
	cmd  *exec.Cmd
	w    *wire
	in   io.WriteCloser
	// stderr is the child's last diagnostics, kept bounded so a chatty plugin
	// cannot grow the host's memory. It is what a crash report reads.
	stderr *boundedBuffer

	exited chan struct{}
	once   sync.Once

	mu       sync.Mutex
	identity subprocess.InitResult
}

// startChild spawns one plugin and completes the SDK handshake.
//
// It returns a started, introduced, loaded child or an error and no process —
// there is no half-started state for a caller to reason about, because a plugin
// that failed its handshake is one this host cannot dispatch to and keeping it
// on the roster would mean every call site checking.
func startChild(ctx context.Context, spec ChildSpec, hostVersion string) (*child, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return nil, fmt.Errorf("pluginhost: plugin %q names no command to spawn", spec.ID)
	}

	// Deliberately not exec.CommandContext: that ties the process's lifetime to
	// this context, which is a boot-time context with a startup budget. The
	// process must outlive its own startup; stop() is what ends it.
	cmd := exec.Command(spec.Command, spec.Args...) // #nosec G204 -- the command is host configuration, not caller input.
	cmd.Dir = spec.WorkDir
	if len(spec.Env) > 0 {
		cmd.Env = append(cmd.Environ(), spec.Env...)
	}
	configureProcessGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("pluginhost: %s: stdin pipe: %w", spec.ID, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("pluginhost: %s: stdout pipe: %w", spec.ID, err)
	}
	stderr := newBoundedBuffer(4096)
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("pluginhost: %s: spawn %s: %w", spec.ID, spec.Command, err)
	}

	c := &child{
		spec: spec, cmd: cmd, in: stdin, stderr: stderr,
		w:      newWire(stdout, stdin),
		exited: make(chan struct{}),
	}
	go func() {
		_ = cmd.Wait()
		close(c.exited)
	}()

	if err := c.handshake(ctx, hostVersion); err != nil {
		// A child that cannot introduce itself is killed rather than left
		// running: it holds whatever dependency it was spawned for, and this
		// host has no way to reach it.
		_ = c.kill()
		return nil, err
	}
	return c, nil
}

// handshake runs plugin/init then plugin/load, per the SDK's lifecycle.
func (c *child) handshake(ctx context.Context, hostVersion string) error {
	ctx, cancel := context.WithTimeout(ctx, childHandshakeBudget)
	defer cancel()

	raw, err := c.w.call(ctx, subprocess.MethodInit, subprocess.InitParams{
		PluginDir: c.spec.WorkDir,
		DataDir:   c.spec.DataDir,
		CacheDir:  c.spec.CacheDir,

		// EMPTY, ALWAYS, AND THAT IS THE DECISION RATHER THAN AN OVERSIGHT.
		//
		// The SDK's ConfigReader is documented as the plugin's view of config
		// "the host resolves via its precedence rules (plugin_settings DB ->
		// env var -> plugin.yaml default)". Tangent has no such store and will
		// not grow one: GetConfig, SetConfig and RegisterConfigSchema are
		// ratified unimplemented (CW-20260910-0036), because a host that holds
		// no plugin configuration can never hold a plugin's secret. That keeps
		// ADR 0005 §3.1's boundary true by construction rather than by policy.
		//
		// So a plugin reads its own environment, exactly as the compiled-in
		// ones do — torque.New() and tesseract.New() both call os.Getenv at
		// construction, and subprocess mode changes nothing about that. Pass
		// what a plugin needs through ChildSpec.Env, where it goes to the
		// child's process and is never held here.
		//
		// Do not "fix" this by plumbing a config map through. The emptiness is
		// the boundary.
		Config: map[string]string{},

		LogLevel: "info",
		HostInfo: subprocess.HostInfo{
			Version:  hostVersion,
			Protocol: subprocess.ProtocolVersion,
		},
	})
	if err != nil {
		return fmt.Errorf("pluginhost: %s: init: %w%s", c.spec.ID, err, c.diagnostics())
	}
	var identity subprocess.InitResult
	if err := json.Unmarshal(raw, &identity); err != nil {
		return fmt.Errorf("pluginhost: %s: decode init result: %w", c.spec.ID, err)
	}
	if identity.Protocol != subprocess.ProtocolVersion {
		return fmt.Errorf(
			"pluginhost: %s speaks wire protocol %d, this host speaks %d; "+
				"the handshake is exact rather than ranged, so one of the two needs rebuilding",
			c.spec.ID, identity.Protocol, subprocess.ProtocolVersion)
	}
	if identity.ID == "" {
		return fmt.Errorf("pluginhost: %s returned no id from init", c.spec.ID)
	}

	c.mu.Lock()
	c.identity = identity
	c.mu.Unlock()

	if _, err := c.w.call(ctx, subprocess.MethodLoad, subprocess.LoadParams{}); err != nil {
		return fmt.Errorf("pluginhost: %s: load: %w%s", c.spec.ID, err, c.diagnostics())
	}
	return nil
}

// call dispatches one request to the child.
func (c *child) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.w.call(ctx, method, params)
}

// info returns what the child said it was at init.
func (c *child) info() subprocess.InitResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.identity
}

// stop ends the child: ask, then close its stdin, then wait, then kill.
//
// It always returns having either observed the exit or issued a SIGKILL to the
// process group, within childUnloadBudget + childReapBudget. It never waits on
// a plugin indefinitely, which is the property UnloadAll needs from it.
func (c *child) stop() error {
	// The result is deliberately ignored. A plugin that refuses or fails to
	// answer plugin/unload is exactly the one the stronger signals below exist
	// for, and treating its refusal as an error here would mean reporting a
	// failure for a child that is about to be shut down correctly anyway.
	func() {
		ctx, cancel := context.WithTimeout(context.Background(), childUnloadBudget)
		defer cancel()
		_, _ = c.w.call(ctx, subprocess.MethodUnload, nil)
	}()

	// Closing stdin is the SDK's own shutdown path — Serve's reader loop ends
	// on EOF and calls the plugin's Unload. It is sent whether or not the
	// unload call above succeeded, because a plugin that failed to answer is
	// exactly the one that needs the stronger signal.
	if c.in != nil {
		_ = c.in.Close()
	}

	select {
	case <-c.exited:
		return nil
	case <-time.After(childUnloadBudget):
	}

	if err := c.kill(); err != nil {
		return fmt.Errorf("pluginhost: %s: kill after %s: %w", c.spec.ID, childUnloadBudget, err)
	}
	select {
	case <-c.exited:
	case <-time.After(childReapBudget):
		return fmt.Errorf(
			"pluginhost: %s did not exit %s after SIGKILL; a grandchild is probably holding "+
				"the pipe. Continuing rather than holding shutdown%s",
			c.spec.ID, childReapBudget, c.diagnostics())
	}
	return nil
}

// kill signals the child's whole process group.
func (c *child) kill() error {
	var err error
	c.once.Do(func() {
		if c.cmd.Process == nil {
			return
		}
		err = killProcessGroup(c.cmd.Process.Pid)
	})
	return err
}

// diagnostics returns the child's recent stderr, for an error message that
// would otherwise say only that something failed.
func (c *child) diagnostics() string {
	tail := strings.TrimSpace(c.stderr.String())
	if tail == "" {
		return ""
	}
	return "\nplugin stderr:\n" + tail
}

// boundedBuffer keeps the last n bytes written to it. A plugin's stderr is
// useful for a crash report and unbounded by nature, so it is bounded here
// rather than trusted.
type boundedBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func newBoundedBuffer(limit int) *boundedBuffer {
	return &boundedBuffer{limit: limit}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.limit {
		b.buf = b.buf[len(b.buf)-b.limit:]
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
