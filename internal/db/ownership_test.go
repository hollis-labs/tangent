package db

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const holdLockEnv = "TANGENT_TEST_HOLD_LOCK"

// TestHelperHoldsOwnership is not a test. It is the second process the
// single-writer contract is actually about: `go test` re-executes its own
// binary with the environment variable set, that process takes the lock and
// waits, and the real test asserts that this process is refused.
//
// A same-process test with two file descriptors would also conflict — flock is
// per open file description — but it would prove a weaker thing than the
// contract claims.
func TestHelperHoldsOwnership(t *testing.T) {
	path := os.Getenv(holdLockEnv)
	if path == "" {
		t.Skip("helper process only")
	}
	owner, err := AcquireOwnership(path, RoleServer, "test-helper")
	if err != nil {
		t.Fatalf("helper could not acquire: %v", err)
	}
	defer func() { _ = owner.Release() }()
	// Announce on stdout so the parent knows the lock is held before it tries.
	if _, err := os.Stdout.WriteString("held\n"); err != nil {
		t.Fatalf("announce: %v", err)
	}
	time.Sleep(3 * time.Second)
}

func TestSecondProcessIsRefusedWithTheHolderNamed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owned.db")

	//nolint:gosec // re-executing this test binary is the point: the contract is between processes
	helper := exec.Command(os.Args[0], "-test.run", "^TestHelperHoldsOwnership$", "-test.v")
	helper.Env = append(os.Environ(), holdLockEnv+"="+path)
	stdout, err := helper.StdoutPipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if startErr := helper.Start(); startErr != nil {
		t.Fatalf("start helper: %v", startErr)
	}
	// Only this process's own child is ever signaled — never a pattern match.
	t.Cleanup(func() {
		_ = helper.Process.Kill()
		_ = helper.Wait()
	})

	buffer := make([]byte, 4096)
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(string(buffer), "held") && time.Now().Before(deadline) {
		n, readErr := stdout.Read(buffer)
		if readErr != nil || n == 0 {
			break
		}
	}
	if !strings.Contains(string(buffer), "held") {
		t.Fatalf("helper never announced the lock; output was %q", string(buffer))
	}

	_, err = AcquireOwnership(path, RoleMaintenance, "tangent --db-repair")
	if err == nil {
		t.Fatal("a second process acquired the single-writer lock")
	}
	var conflict *OwnershipConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("expected an OwnershipConflict, got %T: %v", err, err)
	}
	if !conflict.HolderKnown {
		t.Fatalf("the refusal did not identify the holder: %v", conflict)
	}
	if conflict.Holder.Role != RoleServer || conflict.Holder.PID == os.Getpid() {
		t.Fatalf("the refusal named the wrong holder: %+v", conflict.Holder)
	}
	if !strings.Contains(conflict.Error(), "pid") {
		t.Fatalf("the refusal message does not name a process: %s", conflict.Error())
	}

	holder, held, err := InspectOwnership(path)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !held || holder.Role != RoleServer {
		t.Fatalf("inspect did not see the holder: held=%v holder=%+v", held, holder)
	}
}

func TestReleasedOwnershipCanBeReacquired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cycled.db")

	first, err := AcquireOwnership(path, RoleMaintenance, "first")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if releaseErr := first.Release(); releaseErr != nil {
		t.Fatalf("release: %v", releaseErr)
	}
	// Releasing twice is a no-op, so a deferred release next to an explicit one
	// is not a bug.
	if releaseErr := first.Release(); releaseErr != nil {
		t.Fatalf("second release: %v", releaseErr)
	}

	second, err := AcquireOwnership(path, RoleServer, "second")
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	defer func() { _ = second.Release() }()

	if _, held, err := InspectOwnership(path); err != nil || !held {
		t.Fatalf("inspect after reacquire: held=%v err=%v", held, err)
	}
}

// TestStaleLockFileDoesNotBlock is the crash-recovery half. A process that is
// killed leaves the sidecar behind, and the kernel drops the lock; the next
// acquirer must succeed without an operator deleting anything.
func TestStaleLockFileDoesNotBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale.db")
	lockPath := path + ownerSuffix

	stale := `{"pid":999999,"role":"server","host":"gone","started_at":"2020-01-01T00:00:00Z"}`
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(lockPath, []byte(stale), 0o600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}

	holder, held, err := InspectOwnership(path)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if held {
		t.Fatalf("a stale file with no kernel lock reported held: %+v", holder)
	}
	owner, err := AcquireOwnership(path, RoleMaintenance, "recovering")
	if err != nil {
		t.Fatalf("a stale lock file blocked acquisition: %v", err)
	}
	defer func() { _ = owner.Release() }()

	record, held, err := InspectOwnership(path)
	if err != nil || !held {
		t.Fatalf("inspect after recovery: held=%v err=%v", held, err)
	}
	if record.PID != os.Getpid() {
		t.Fatalf("the stale record was not overwritten: %+v", record)
	}
}

func TestInMemoryDatabaseTakesNoLock(t *testing.T) {
	owner, err := AcquireOwnership(":memory:", RoleMaintenance, "memory")
	if err != nil {
		t.Fatalf("acquire in-memory: %v", err)
	}
	if err := owner.Release(); err != nil {
		t.Fatalf("release in-memory: %v", err)
	}
	if _, held, err := InspectOwnership(":memory:"); err != nil || held {
		t.Fatalf("in-memory reported ownership: held=%v err=%v", held, err)
	}
}
