package effect

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/tangent/internal/authz"
	tangentdb "github.com/hollis-labs/tangent/internal/db"
)

// TestSelectionNeverImpliesRead is acceptance criterion 1.
//
// Minting a handle is what "selecting a file" becomes. The test asserts that a
// handle, on its own, causes nothing: the same handle is refused four times
// for four independent reasons, and only when every one of them is satisfied
// does a byte move.
func TestSelectionNeverImpliesRead(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	handle := fixture.mintFileHandle(t, "notes.md", FileReadScoped)

	// 1. The definition never declared the capability.
	got := fixture.request(t, Request{
		Capability:     FileReadScoped,
		Principal:      fixture.participant(),
		Binding:        Binding{BindingDigest: fixture.digest},
		OwnerScope:     authz.ParticipantScope,
		InteractionID:  fixture.interactionID,
		HandleID:       handle.ID,
		IdempotencyKey: "k1",
		Intent:         confirmedIntent(),
	})
	assertRefused(t, got, CodeCapabilityUndeclared)

	// 2. Declared, but this host does not grant it.
	got = fixture.request(t, fixture.readRequest("k2", handle.ID, func(r *Request) {
		r.Binding.Granted = nil
	}))
	assertRefused(t, got, CodeCapabilityDenied)

	// 3. Granted, but the participant expressed no intent.
	got = fixture.request(t, fixture.readRequest("k3", handle.ID, func(r *Request) {
		r.Intent = Intent{}
	}))
	assertRefused(t, got, CodeIntentMissing)

	// 4. Granted and intended, but the participant lacks the object-access
	//    precondition. This is the two namespaces meeting: a session with no
	//    `view` on the object reads nothing however complete its effect grant.
	got = fixture.request(t, fixture.readRequest("k4", handle.ID, func(r *Request) {
		r.Principal.Granted = []authz.Capability{authz.Draft}
	}))
	assertRefused(t, got, CodeNotAuthorized)

	// Everything satisfied: the effect happens, and only now.
	got = fixture.request(t, fixture.readRequest("k5", handle.ID, nil))
	if got.Receipt.Decision != DecisionGranted {
		t.Fatalf("fully authorized read = %s/%s, want granted", got.Receipt.Decision, got.Receipt.Code)
	}
	if string(got.Content) != "the file's content" {
		t.Fatalf("read returned %q", got.Content)
	}
}

// TestAnArbitraryActionIdentifierGrantsNoEffect is acceptance criterion 2.
//
// The shipped workflows echo caller-authored `action_id` and `control_id`
// strings back into decision records. This asserts what those strings are: an
// audit label. A renderer that names an effect it was not granted, or invents
// a capability id outright, reaches nothing — and the invented id does not even
// get an audit row, because a request with no recognizable capability has no
// row shape.
func TestAnArbitraryActionIdentifierGrantsNoEffect(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	handle := fixture.mintFileHandle(t, "notes.md", FileReadScoped)

	invented := fixture.request(t, fixture.readRequest("k1", handle.ID, func(r *Request) {
		r.Capability = "file.read_everything"
		r.Intent.ControlID = "admin-override"
	}))
	assertRefused(t, invented, CodeInvalidRequest)

	// A wildly suggestive control id changes nothing about a capability the
	// definition does hold: it is recorded on the receipt and dispatched on
	// nowhere.
	granted := fixture.request(t, fixture.readRequest("k2", handle.ID, func(r *Request) {
		r.Intent.ControlID = "../../etc/passwd"
	}))
	if granted.Receipt.Decision != DecisionGranted {
		t.Fatalf("a strange control id changed the decision: %s/%s",
			granted.Receipt.Decision, granted.Receipt.Code)
	}
	if string(granted.Content) != "the file's content" {
		t.Fatalf("a control id selected a different file: %q", granted.Content)
	}
}

// TestAHandleIsScopedToItsRealmInteractionAndBinding. Every mismatch returns
// the same code an unknown handle returns, so a refusal cannot be used to
// enumerate handles that exist.
func TestAHandleIsScopedToItsRealmInteractionAndBinding(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	handle := fixture.mintFileHandle(t, "notes.md", FileReadScoped)

	for name, mutate := range map[string]func(*Request){
		"another interaction": func(r *Request) { r.InteractionID = "surface-2/interaction-9" },

		"another binding":  func(r *Request) { r.Binding.BindingDigest = "sha256:different" },
		"unknown id":       func(r *Request) { r.HandleID = "eh_00000000-0000-0000-0000-000000000000" },
		"no handle at all": func(r *Request) { r.HandleID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			got := fixture.request(t, fixture.readRequest("key-"+name, handle.ID, mutate))
			assertRefused(t, got, CodeHandleUnknown)
		})
	}

	// A handle minted in another realm is indistinguishable from one that does
	// not exist. This is the enumeration case, and it is separate from a
	// cross-realm *principal*, which authz.Authorize refuses one step earlier.
	foreign, _, err := fixture.broker.Mint(context.Background(), Mint{
		Class:            ClassWorkspaceFile,
		RootID:           "ws-alpha",
		RelativePath:     "notes.md",
		InteractionID:    fixture.interactionID,
		ParticipantScope: "gateway:elsewhere",
		BindingDigest:    fixture.digest,
		Capabilities:     []Capability{FileReadScoped},
	})
	if err != nil {
		t.Fatalf("mint a foreign handle: %v", err)
	}
	assertRefused(t, fixture.request(t, fixture.readRequest("foreign", foreign.ID, nil)), CodeHandleUnknown)

	// A cross-realm principal never reaches the handle at all: the
	// object-access namespace refuses it first, which is the correct order.
	assertRefused(t, fixture.request(t, fixture.readRequest("cross-realm", handle.ID, func(r *Request) {
		r.Principal.Scope = "gateway:elsewhere"
	})), CodeNotAuthorized)

	// A handle minted for reading does not cover writing, even where the
	// definition holds both grants.
	got := fixture.request(t, fixture.readRequest("write-with-a-read-handle", handle.ID, func(r *Request) {
		r.Capability = FileWriteScoped
		r.Binding.Required = append(r.Binding.Required, FileWriteScoped)
		r.Binding.Granted = append(r.Binding.Granted, FileWriteScoped)
		r.Principal.Granted = append(r.Principal.Granted, authz.Draft)
	}))
	assertRefused(t, got, CodeHandleUnknown)
}

// TestAHandleExpires. The window is short by design and the refusal is its own
// code, because an expired handle is the one refusal a participant can fix by
// doing the thing again.
func TestAHandleExpires(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	handle, _, err := fixture.broker.Mint(context.Background(), Mint{
		Class:            ClassWorkspaceFile,
		RootID:           "ws-alpha",
		RelativePath:     "notes.md",
		InteractionID:    fixture.interactionID,
		ParticipantScope: authz.ParticipantScope,
		BindingDigest:    fixture.digest,
		Capabilities:     []Capability{FileReadScoped},
		Lifetime:         time.Minute,
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	got := fixture.request(t, fixture.readRequest("before", handle.ID, nil))
	if got.Receipt.Decision != DecisionGranted {
		t.Fatalf("read before expiry = %s/%s", got.Receipt.Decision, got.Receipt.Code)
	}

	fixture.advance(2 * time.Minute)
	got = fixture.request(t, fixture.readRequest("after", handle.ID, nil))
	assertRefused(t, got, CodeHandleExpired)
}

// TestGrantedUseIsSpentOnlyBySuccess. A refusal must not burn a participant's
// budget: a single-use export that is refused for a reason they can fix has to
// remain usable once they fix it.
func TestGrantedUseIsSpentOnlyBySuccess(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	handle, _, err := fixture.broker.Mint(context.Background(), Mint{
		Class:            ClassWorkspaceFile,
		RootID:           "ws-alpha",
		RelativePath:     "notes.md",
		InteractionID:    fixture.interactionID,
		ParticipantScope: authz.ParticipantScope,
		BindingDigest:    fixture.digest,
		Capabilities:     []Capability{FileReadScoped},
		MaxUses:          1,
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	// A refusal the participant can fix.
	refused := fixture.request(t, fixture.readRequest("no-intent", handle.ID, func(r *Request) {
		r.Intent = Intent{}
	}))
	assertRefused(t, refused, CodeIntentMissing)

	// The budget survived it.
	first := fixture.request(t, fixture.readRequest("first", handle.ID, nil))
	if first.Receipt.Decision != DecisionGranted {
		t.Fatalf("a refusal spent the granted use: %s/%s", first.Receipt.Decision, first.Receipt.Code)
	}
	// And is now spent.
	second := fixture.request(t, fixture.readRequest("second", handle.ID, nil))
	assertRefused(t, second, CodeHandleExhausted)
}

// TestIdempotencyReplaysRatherThanRepeating. A replayed key returns the stored
// receipt and no content — the honest meaning of "you already did this" — and
// a key reused for a different request is a conflict rather than a silent
// second act.
func TestIdempotencyReplaysRatherThanRepeating(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	handle := fixture.mintFileHandle(t, "notes.md", FileReadScoped)
	other := fixture.mintFileHandle(t, "other.md", FileReadScoped)

	first := fixture.request(t, fixture.readRequest("shared", handle.ID, nil))
	if first.Receipt.Decision != DecisionGranted || len(first.Content) == 0 {
		t.Fatalf("first request = %s/%s with %d bytes", first.Receipt.Decision, first.Receipt.Code, len(first.Content))
	}

	replay := fixture.request(t, fixture.readRequest("shared", handle.ID, nil))
	if !replay.Receipt.Replayed || replay.Receipt.ID != first.Receipt.ID {
		t.Errorf("replay returned receipt %q (replayed=%v), want the stored %q",
			replay.Receipt.ID, replay.Receipt.Replayed, first.Receipt.ID)
	}
	if len(replay.Content) != 0 {
		t.Error("a replay returned content; it must return the receipt only")
	}

	conflict := fixture.request(t, fixture.readRequest("shared", other.ID, nil))
	assertRefused(t, conflict, CodeIdempotencyConflict)
}

// TestEveryRefusalIsAudited. An audit trail that records only successes cannot
// answer the question anyone asks it.
func TestEveryRefusalIsAudited(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	handle := fixture.mintFileHandle(t, "notes.md", FileReadScoped)

	fixture.request(t, fixture.readRequest("refused", handle.ID, func(r *Request) {
		r.Intent = Intent{}
	}))
	fixture.request(t, fixture.readRequest("granted", handle.ID, nil))

	var refusals, grants int
	if err := fixture.db.QueryRow(
		`SELECT
		   (SELECT COUNT(*) FROM effect_receipts WHERE decision = 'refused'),
		   (SELECT COUNT(*) FROM effect_receipts WHERE decision = 'granted')`,
	).Scan(&refusals, &grants); err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	if refusals != 1 || grants != 1 {
		t.Fatalf("receipts = %d refused / %d granted, want 1 / 1", refusals, grants)
	}
}

// TestAReceiptIsImmutable. A record of what a host was made to do is worth
// nothing if the host can edit it afterwards.
func TestAReceiptIsImmutable(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	handle := fixture.mintFileHandle(t, "notes.md", FileReadScoped)
	got := fixture.request(t, fixture.readRequest("k", handle.ID, nil))

	_, err := fixture.db.Exec(
		`UPDATE effect_receipts SET decision = 'refused' WHERE id = ?`, got.Receipt.ID)
	if err == nil {
		t.Fatal("a receipt was rewritten; the immutability trigger is not in force")
	}
	if !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("update refused with %v, want the immutability trigger", err)
	}
}

// TestNoGrantMaterialReachesAReceiptOrAView is acceptance criterion 4 at the
// two surfaces a renderer and an operator actually see.
//
// A receipt is the densest audit surface in the process and the one most
// likely to be read out loud; a View is what crosses into the browser. Neither
// may carry a path, a root id, a filesystem location, or a session. The test
// serializes both and searches the JSON, rather than checking named fields,
// because "the value is not in the field we meant" is a weaker claim than "the
// value is nowhere".
func TestNoGrantMaterialReachesAReceiptOrAView(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)

	handle, view, err := fixture.broker.Mint(context.Background(), Mint{
		Class:            ClassWorkspaceFile,
		RootID:           "ws-alpha",
		RelativePath:     "secret-directory/notes.md",
		InteractionID:    fixture.interactionID,
		ParticipantScope: authz.ParticipantScope,
		BindingDigest:    fixture.digest,
		Capabilities:     []Capability{FileReadScoped},
		Label:            "notes.md",
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	got := fixture.request(t, fixture.readRequest("k", handle.ID, nil))

	forbidden := map[string]string{
		"the root's host path":  fixture.root,
		"the root id":           "ws-alpha",
		"the relative path":     "secret-directory/notes.md",
		"a path fragment":       "secret-directory",
		"the participant realm": "sha256-of-a-cookie",
	}
	for _, surface := range []struct {
		name  string
		value any
	}{
		{"receipt", got.Receipt},
		{"handle view", view},
	} {
		encoded, marshalErr := json.Marshal(surface.value)
		if marshalErr != nil {
			t.Fatalf("marshal %s: %v", surface.name, marshalErr)
		}
		for label, needle := range forbidden {
			if strings.Contains(string(encoded), needle) {
				t.Errorf("%s carries %s (%q): %s", surface.name, label, needle, encoded)
			}
		}
	}

	// The receipt does carry the *digest* of what was read. A digest is an
	// identity, not a payload, and it is what makes a receipt verifiable.
	if got.Receipt.ContentSHA256 == "" || got.Receipt.Bytes == 0 {
		t.Error("a granted read produced no content digest; the receipt is not verifiable")
	}
	if strings.Contains(string(got.Content), "") && got.Receipt.Bytes != int64(len(got.Content)) {
		t.Error("receipt byte count disagrees with the content")
	}
}

// TestNoPathOrRootReachesTheReceiptTable sweeps the durable side of the same
// claim. effect_handles holds a path because that is what a handle *is*;
// effect_receipts — the row that is read, exported, and quoted — holds none.
func TestNoPathOrRootReachesTheReceiptTable(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	handle := fixture.mintFileHandle(t, "notes.md", FileReadScoped)
	fixture.request(t, fixture.readRequest("k", handle.ID, nil))

	rows, err := fixture.db.Query(`SELECT * FROM effect_receipts`)
	if err != nil {
		t.Fatalf("read receipts: %v", err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	for _, column := range columns {
		if strings.Contains(column, "path") || strings.Contains(column, "root") {
			t.Errorf("effect_receipts has a %q column; a receipt must name a handle, not a location", column)
		}
	}
	for rows.Next() {
		cells := make([]any, len(columns))
		targets := make([]any, len(columns))
		for index := range cells {
			targets[index] = &cells[index]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatalf("scan receipt: %v", err)
		}
		for index, cell := range cells {
			text, ok := cell.(string)
			if !ok {
				if raw, isBytes := cell.([]byte); isBytes {
					text, ok = string(raw), true
				}
			}
			if ok && (strings.Contains(text, fixture.root) || strings.Contains(text, "notes.md")) {
				t.Errorf("effect_receipts.%s carries filesystem material: %q", columns[index], text)
			}
		}
	}
}

// TestProcessExecIsAlwaysRefused. Two independent rules refuse it, and the
// test grants everything the model can grant to prove neither is doing all the
// work alone.
func TestProcessExecIsAlwaysRefused(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	got := fixture.request(t, Request{
		Capability: ProcessExec,
		Principal: Principal{
			Kind:    authz.KindParticipant,
			Scope:   authz.ParticipantScope,
			Granted: authz.DefaultParticipantCapabilities(),
		},
		Binding: Binding{
			BindingDigest: fixture.digest,
			Required:      []Capability{ProcessExec},
			Granted:       []Capability{ProcessExec},
		},
		OwnerScope:     authz.ParticipantScope,
		InteractionID:  fixture.interactionID,
		IdempotencyKey: "exec",
		Intent:         confirmedIntent(),
	})
	// The object-access precondition refuses it first: `administer` is not in
	// any participant's grant set.
	assertRefused(t, got, CodeNotAuthorized)
}

// TestADeclaredCapabilityIsAdmittedButNotPerformed records the honest limit in
// the type system's own terms. `clipboard.write` is admitted, audited, and
// performed by the browser — the receipt says `declared`, and nothing in this
// process claims to have enforced it.
func TestADeclaredCapabilityIsAdmittedButNotPerformed(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	got := fixture.request(t, Request{
		Capability: ClipboardWrite,
		Principal:  fixture.participant(),
		Binding: Binding{
			BindingDigest: fixture.digest,
			Required:      []Capability{ClipboardWrite},
			Granted:       []Capability{ClipboardWrite},
		},
		OwnerScope:     authz.ParticipantScope,
		InteractionID:  fixture.interactionID,
		IdempotencyKey: "clip",
		Intent:         confirmedIntent(),
	})
	if got.Receipt.Decision != DecisionGranted {
		t.Fatalf("clipboard.write = %s/%s, want granted", got.Receipt.Decision, got.Receipt.Code)
	}
	if got.Receipt.Mediation != MediationDeclared {
		t.Fatalf("clipboard.write mediation = %q, want %q — the receipt must not claim enforcement",
			got.Receipt.Mediation, MediationDeclared)
	}
	if got.Receipt.Bytes != 0 {
		t.Error("the host reported doing work for a capability the browser performs")
	}
}

// TestAMintRefusesAnUnmediatedRoot. A caller-declared `browse_roots[].path`
// never becomes a mediatable root; only an authority's registration does.
func TestAMintRefusesAnUnmediatedRoot(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t)
	_, _, err := fixture.broker.Mint(context.Background(), Mint{
		Class:            ClassWorkspaceFile,
		RootID:           "whatever-the-caller-called-it",
		RelativePath:     "notes.md",
		InteractionID:    fixture.interactionID,
		ParticipantScope: authz.ParticipantScope,
		Capabilities:     []Capability{FileReadScoped},
	})
	if err == nil {
		t.Fatal("a handle was minted against a root no authority registered")
	}
	// And a traversal is refused at mint, before it can ever be stored.
	if _, _, err := fixture.broker.Mint(context.Background(), Mint{
		Class:            ClassWorkspaceFile,
		RootID:           "ws-alpha",
		RelativePath:     "../../etc/passwd",
		InteractionID:    fixture.interactionID,
		ParticipantScope: authz.ParticipantScope,
		Capabilities:     []Capability{FileReadScoped},
	}); err == nil {
		t.Fatal("a traversing handle was minted")
	}
}

// ── fixture ────────────────────────────────────────────────────────────

type fixture struct {
	broker        *Broker
	db            *sql.DB
	root          string
	digest        string
	interactionID string
	clock         time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	database, err := tangentdb.Open(filepath.Join(t.TempDir(), "effect.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrateErr := tangentdb.RunMigrations(database); migrateErr != nil {
		t.Fatalf("run migrations: %v", migrateErr)
	}
	store, err := NewSQLStore(database)
	if err != nil {
		t.Fatalf("NewSQLStore: %v", err)
	}

	root := t.TempDir()
	if writeErr := os.WriteFile(
		filepath.Join(root, "notes.md"), []byte("the file's content"), 0o600,
	); writeErr != nil {
		t.Fatalf("seed root: %v", writeErr)
	}
	if writeErr := os.WriteFile(
		filepath.Join(root, "other.md"), []byte("another file"), 0o600,
	); writeErr != nil {
		t.Fatalf("seed root: %v", writeErr)
	}
	if mkdirErr := os.MkdirAll(filepath.Join(root, "secret-directory"), 0o750); mkdirErr != nil {
		t.Fatalf("seed root: %v", mkdirErr)
	}
	if writeErr := os.WriteFile(
		filepath.Join(root, "secret-directory", "notes.md"), []byte("nested content"), 0o600,
	); writeErr != nil {
		t.Fatalf("seed root: %v", writeErr)
	}

	f := &fixture{
		db:            database,
		root:          root,
		digest:        "sha256:binding",
		interactionID: "surface-1/interaction-1",
		clock:         time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
	}
	broker, err := NewBroker(
		store,
		generousAuthority{
			grants: []Capability{FileReadScoped, FileWriteScoped, ClipboardWrite},
			roots:  []Root{{ID: "ws-alpha", Label: "Workspace", Path: root, Writable: true}},
		},
		WithClock(func() time.Time { return f.clock }),
	)
	if err != nil {
		t.Fatalf("NewBroker: %v", err)
	}
	f.broker = broker
	return f
}

func (f *fixture) advance(d time.Duration) { f.clock = f.clock.Add(d) }

func (f *fixture) participant() Principal {
	return Principal{
		Kind:    authz.KindParticipant,
		Scope:   authz.ParticipantScope,
		Granted: authz.DefaultParticipantCapabilities(),
	}
}

func (f *fixture) mintFileHandle(t *testing.T, relative string, capabilities ...Capability) Handle {
	t.Helper()
	handle, _, err := f.broker.Mint(context.Background(), Mint{
		Class:            ClassWorkspaceFile,
		RootID:           "ws-alpha",
		RelativePath:     relative,
		InteractionID:    f.interactionID,
		ParticipantScope: authz.ParticipantScope,
		BindingDigest:    f.digest,
		Capabilities:     capabilities,
	})
	if err != nil {
		t.Fatalf("Mint %s: %v", relative, err)
	}
	return handle
}

func (f *fixture) readRequest(key, handleID string, mutate func(*Request)) Request {
	request := Request{
		Capability: FileReadScoped,
		Principal:  f.participant(),
		Binding: Binding{
			BindingDigest: f.digest,
			Required:      []Capability{FileReadScoped},
			Granted:       []Capability{FileReadScoped},
		},
		OwnerScope:     authz.ParticipantScope,
		InteractionID:  f.interactionID,
		HandleID:       handleID,
		IdempotencyKey: key,
		Intent:         confirmedIntent(),
	}
	if mutate != nil {
		mutate(&request)
	}
	return request
}

func (f *fixture) request(t *testing.T, request Request) Result {
	t.Helper()
	result, err := f.broker.Request(context.Background(), request)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	return result
}

func confirmedIntent() Intent {
	return Intent{
		ControlID:         "preview-selected-file",
		PresentedRevision: 3,
		ConfirmedAt:       time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
	}
}

func assertRefused(t *testing.T, got Result, code string) {
	t.Helper()
	if got.Receipt.Decision != DecisionRefused || got.Receipt.Code != code {
		t.Fatalf("decision = %s/%s, want refused/%s", got.Receipt.Decision, got.Receipt.Code, code)
	}
	if len(got.Content) != 0 {
		t.Fatalf("a refused effect returned %d bytes of content", len(got.Content))
	}
}
