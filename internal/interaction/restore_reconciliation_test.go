package interaction

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

// TestRestoredDatabaseReconcilesDeliveryObligations is the half of criterion 2
// that a fingerprint cannot prove on its own.
//
// A fingerprint can say that the delivery rows survived the round trip. It
// cannot say that the process which opens the restored database will do
// anything with them — and an obligation that survives the copy but is never
// picked up again is indistinguishable, from the caller's side, from one that
// was dropped. So this test does the whole thing: builds real interactions
// through the service, interrupts their deliveries the way a killed process
// would, backs the database up, restores it somewhere else, and runs the same
// restart recovery the binary runs at boot against the restored file.
//
// It lives here rather than in internal/db because it needs the service to
// create the records and to reconcile them, and because a restore that
// preserved rows the recovery path cannot use would be a passing test in the
// wrong package.
func TestRestoredDatabaseReconcilesDeliveryObligations(t *testing.T) {
	ctx := context.Background()
	store, databasePath := openTestStore(t)
	service := newTestService(t, store)
	surface := mustOpenSurface(t, service, "restore-reconciliation")

	// An interrupted resolution delivery: the worker took a lease and died.
	interrupted := resolveForRecovery(t, service, surface.SurfaceID, "restore-interrupted", nil)
	markDeliveryDelivering(t, store.db, "resolution_deliveries", interrupted.Deliveries[0].ID)

	// One that needs a human, because "manual" and "retryable" must not be
	// collapsed by a restore any more than by a restart.
	manual := resolveForRecovery(t, service, surface.SurfaceID, "restore-manual", []ResolutionDeliveryParams{{
		DestinationBinding: json.RawMessage(`{"kind":"external","destination":"opaque"}`),
		IdempotencyKey:     "external:restore-manual",
		Policy:             json.RawMessage(`{"restart_recovery":"manual"}`),
	}})
	markDeliveryDelivering(t, store.db, "resolution_deliveries", manual.Deliveries[0].ID)

	// A canceled interaction with an interrupted terminal notification.
	canceled := mustSubmit(t, service, surface.SurfaceID, "restore-canceled")
	if _, err := service.CancelInteraction(ctx, CancelInteractionInput{
		InteractionID: canceled.InteractionID, ExpectedRevision: canceled.Revision,
		Requester: testCaller(), Cause: TerminalCauseCallerCanceled,
	}); err != nil {
		t.Fatalf("CancelInteraction: %v", err)
	}
	notifications, err := store.ListTerminalNotifications(ctx, canceled.InteractionID)
	if err != nil || len(notifications) != 1 {
		t.Fatalf("terminal notifications = %#v, %v", notifications, err)
	}
	markDeliveryDelivering(t, store.db, "terminal_notifications", notifications[0].ID)

	// A still-open interaction, so the restore has non-terminal work to carry.
	presented := presentForRecovery(t, service, surface.SurfaceID, "restore-presented")

	sourceFingerprint, err := tangentdb.TakeFingerprint(ctx, store.db)
	if err != nil {
		t.Fatalf("fingerprint source: %v", err)
	}
	if sourceFingerprint.PendingDeliveries == 0 || sourceFingerprint.PendingTerminalNotifications == 0 {
		t.Fatalf("the fixture has no pending obligations to preserve: %+v", sourceFingerprint)
	}

	backupPath := filepath.Join(t.TempDir(), "obligations.db")
	if _, backupErr := tangentdb.Backup(
		ctx, store.db, databasePath, backupPath, tangentdb.BackupOnline); backupErr != nil {
		t.Fatalf("backup: %v", backupErr)
	}

	restoredPath := filepath.Join(t.TempDir(), "restored.db")
	restore, err := tangentdb.Restore(ctx, restoredPath, backupPath)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(restore.Differences) != 0 {
		t.Fatalf("restore did not preserve every property: %+v", restore.Differences)
	}
	if restore.PendingDeliveries != sourceFingerprint.PendingDeliveries ||
		restore.PendingTerminalNotifications != sourceFingerprint.PendingTerminalNotifications {
		t.Fatalf("pending obligations changed across the restore: %d/%d became %d/%d",
			sourceFingerprint.PendingDeliveries, sourceFingerprint.PendingTerminalNotifications,
			restore.PendingDeliveries, restore.PendingTerminalNotifications)
	}
	if restore.LeasesHeld == 0 {
		t.Fatal("the dead worker's leases did not survive; restart recovery would have nothing to release")
	}

	// Open the restored file the way the binary does at boot and reconcile.
	restoredDB, err := tangentdb.Open(restoredPath)
	if err != nil {
		t.Fatalf("open restored: %v", err)
	}
	t.Cleanup(func() { _ = restoredDB.Close() })
	restoredService := newTestService(t, NewStore(restoredDB))

	report, err := restoredService.RecoverAfterRestart(ctx)
	if err != nil {
		t.Fatalf("RecoverAfterRestart on the restored database: %v", err)
	}
	if report.InterruptedDeliveries != 3 {
		t.Fatalf("expected the three interrupted obligations to be reconciled, got %d (%+v)",
			report.InterruptedDeliveries, report)
	}
	if report.ImmediatelyRetryable == 0 {
		t.Fatal("no obligation was made retryable; the caller would wait forever")
	}
	if report.ManualReconciliationRequired == 0 {
		t.Fatal("the manual-reconciliation obligation was silently retried")
	}
	if report.Presented == 0 {
		t.Fatalf("the still-open interaction %s was not carried across the restore",
			presented.InteractionID)
	}

	// Reconciliation must not have destroyed the record identity it acted on.
	afterRecovery, err := tangentdb.TakeFingerprint(ctx, restoredDB)
	if err != nil {
		t.Fatalf("fingerprint after recovery: %v", err)
	}
	for _, property := range []struct {
		name   string
		before tangentdb.Property
		after  tangentdb.Property
	}{
		{"definition digests", sourceFingerprint.DefinitionDigests, afterRecovery.DefinitionDigests},
		{"interaction identities", sourceFingerprint.InteractionIdentities, afterRecovery.InteractionIdentities},
		{"resolutions", sourceFingerprint.Resolutions, afterRecovery.Resolutions},
	} {
		if !property.before.Equal(property.after) {
			t.Errorf("restart recovery changed %s: %+v -> %+v",
				property.name, property.before, property.after)
		}
	}

	// A second recovery is a no-op, so a restore followed by two boots does not
	// double-reconcile.
	second, err := restoredService.RecoverAfterRestart(ctx)
	if err != nil {
		t.Fatalf("second RecoverAfterRestart: %v", err)
	}
	if second.InterruptedDeliveries != 0 {
		t.Fatalf("recovery was not idempotent: %d obligations reconciled twice",
			second.InterruptedDeliveries)
	}
}
