package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"testing"

	plugin "github.com/hollis-labs/plugin-sdk"

	"github.com/hollis-labs/tangent/internal/envelope"
	"github.com/hollis-labs/tangent/internal/envelope/extensions"
)

// These tests hold ADR 0007 §4. Each one corresponds to a sentence in that
// section, because a decision recorded in a document and nowhere else is a
// decision the next change can undo without anyone noticing.

func newHost(t *testing.T) (*Host, *envelope.Service) {
	t.Helper()
	svc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	if regErr := extensions.RegisterAll(svc); regErr != nil {
		t.Fatalf("RegisterAll: %v", regErr)
	}
	host, err := New(context.Background(), slog.New(slog.DiscardHandler), svc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return host, svc
}

// newContributingHost is newHost for the tests that need the ADR 0007 §4 door
// to OPEN, and it exists because since CW-20260911-0036 nothing shipped goes
// through it.
//
// `tangent.app-board` used to be the shipped input here, and it should never
// have been: it is a domain-free board shape the host publishes, owns and
// compiles into ui_dist, so it belongs to RegisterAll. Removing it left the
// door's success path with no caller, and a success path nothing exercises is
// how a refusal that should be conditional becomes unconditional without any
// test objecting.
//
// So the kind is fixtured and the resolution is not. The envelope service is
// empty — RegisterAll is deliberately not called — and the substituted door
// installs app-board through extensions.RegisterAppBoard, which reads the real
// manifest out of the real embedded package tree. What is stubbed is the
// registration table's answer to "is this kind contributable", one line whose
// own branches are held by contributedDoorFixture in
// internal/envelope/extensions/register_all_test.go. Everything in
// RegisterUIComponent stays real, and every refusal it makes fires before the
// door is reached, so those tests keep using newHost and the genuine one.
func newContributingHost(t *testing.T) (*Host, *envelope.Service, string) {
	t.Helper()
	svc, err := envelope.New(context.Background())
	if err != nil {
		t.Fatalf("envelope.New: %v", err)
	}
	host, err := New(context.Background(), slog.New(slog.DiscardHandler), svc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	kind := extensions.AppBoardEnvelopeType
	host.contributeKind = func(target *envelope.Service, name string) error {
		if name != kind {
			return fmt.Errorf("%w: %s ships no manifest in this host",
				extensions.ErrNotContributable, name)
		}
		return extensions.RegisterAppBoard(target)
	}
	return host, svc, kind
}

// stubPlugin is a plugin whose Load does exactly what the test tells it to.
type stubPlugin struct {
	id         string
	deps       []string
	component  *plugin.UIComponent
	components []plugin.UIComponent
	loadErr    error
	status     plugin.PluginStatus
}

func (p *stubPlugin) ID() string             { return p.id }
func (p *stubPlugin) Name() string           { return p.id }
func (p *stubPlugin) Version() string        { return "0.0.1" }
func (p *stubPlugin) Description() string    { return "stub" }
func (p *stubPlugin) Dependencies() []string { return p.deps }
func (p *stubPlugin) Unload() error          { return nil }

func (p *stubPlugin) Status() plugin.PluginStatus { return p.status }

func (p *stubPlugin) Load(host plugin.Host) error {
	if p.loadErr != nil {
		return p.loadErr
	}
	for _, component := range p.components {
		if err := host.RegisterUIComponent(component); err != nil {
			return err
		}
	}
	if p.component == nil {
		return nil
	}
	return host.RegisterUIComponent(*p.component)
}

// TestEnvelopeComponentRequiresAManifest is the §4 rule itself: "a registration
// without a manifest is refused". It is the single most important behavior in
// this package — without it, a plugin declaring a kind would put an
// unclassified interaction type into the registry, and EnvelopeRouter's trust
// classification would be deciding about a kind no manifest ever described.
func TestEnvelopeComponentRequiresAManifest(t *testing.T) {
	t.Parallel()
	host, svc := newHost(t)
	before := svc.Len()

	err := host.RegisterUIComponent(plugin.UIComponent{
		ID:   "tangent.invented-by-a-plugin",
		Type: plugin.UIComponentTypeEnvelope,
		Name: "InventedView",
	})
	if !errors.Is(err, ErrNoManifest) {
		t.Fatalf("err = %v, want ErrNoManifest", err)
	}
	if svc.Len() != before {
		t.Errorf("registry grew from %d to %d on a refused registration", before, svc.Len())
	}
	if kinds := host.ContributedKinds(); len(kinds) != 0 {
		t.Errorf("host recorded %v after a refusal", kinds)
	}
}

// TestEnvelopeComponentWithAManifestRegisters is the other half — the rule
// admits the contributable case, so a refusal above means "no manifest" rather
// than "nothing works".
//
// It runs on newContributingHost. Read the comment there before concluding the
// test went soft: the door is fixtured because this host ships no contributable
// kind, and the manifest it resolves is real.
func TestEnvelopeComponentWithAManifestRegisters(t *testing.T) {
	t.Parallel()
	host, svc, kind := newContributingHost(t)

	if err := host.RegisterUIComponent(plugin.UIComponent{
		ID:   kind,
		Type: plugin.UIComponentTypeEnvelope,
		Name: "AppBoardView",
	}); err != nil {
		t.Fatalf("RegisterUIComponent: %v", err)
	}
	spec, found := svc.Lookup(kind)
	if !found {
		t.Fatal("the kind did not reach the registry")
	}
	if spec.Name != kind {
		t.Errorf("registered name = %q", spec.Name)
	}
	if got := host.ContributedKinds(); len(got) != 1 || got[0] != kind {
		t.Errorf("ContributedKinds = %v", got)
	}
}

// TestPluginCannotAuthorHostReservedProperties holds §4's second consequence: a
// plugin cannot author its own trust class, granted capabilities, effective
// assurance, or asset digest. The manifest decides those, and a UIComponent is
// not a second way to claim them.
//
// The refusal is loud rather than a silent downgrade on purpose. A plugin that
// believes it raised its own trust class and was quietly ignored is a worse
// outcome than one that fails to load.
func TestPluginCannotAuthorHostReservedProperties(t *testing.T) {
	t.Parallel()
	for _, key := range []string{
		"trust_class", "trustClass", "granted_capabilities", "assurance",
		"asset_digest", "manifest_digest", "isolation", "draft_custody",
		"retention_class", "required_capabilities",
	} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			host, svc := newHost(t)
			before := svc.Len()
			err := host.RegisterUIComponent(plugin.UIComponent{
				ID:    extensions.AppBoardEnvelopeType,
				Type:  plugin.UIComponentTypeEnvelope,
				Name:  "AppBoardView",
				Props: map[string]interface{}{key: "core-trusted"},
			})
			if !errors.Is(err, ErrReservedProperty) {
				t.Fatalf("err = %v, want ErrReservedProperty", err)
			}
			if svc.Len() != before {
				t.Errorf("a component authoring %q still registered its kind", key)
			}
		})
	}
}

// TestOnlyEnvelopeComponentsAreHonored. The SDK offers five component types;
// Tangent has a surface for one. Accepting the rest to be accommodating would
// mean four registration points recording something nothing reads, which is the
// widening ADR 0007's risk section describes.
func TestOnlyEnvelopeComponentsAreHonored(t *testing.T) {
	t.Parallel()
	for _, kind := range []plugin.UIComponentType{
		plugin.UIComponentTypeWidget,
		plugin.UIComponentTypeAction,
		plugin.UIComponentTypeWorkflow,
		plugin.UIComponentTypeView,
	} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			host, _ := newHost(t)
			err := host.RegisterUIComponent(plugin.UIComponent{
				ID: extensions.AppBoardEnvelopeType, Type: kind, Name: "X",
			})
			if !errors.Is(err, ErrSurfaceNotHonored) {
				t.Fatalf("err = %v, want ErrSurfaceNotHonored", err)
			}
		})
	}
}

// TestServerRenderedHandlerIsRefused. A Handler would be a second rendering
// path that no trust class describes. ADR 0007 §4 excludes runtime asset
// loading; a plugin-served handler is the same hole with a different shape.
func TestServerRenderedHandlerIsRefused(t *testing.T) {
	t.Parallel()
	host, svc := newHost(t)
	before := svc.Len()
	err := host.RegisterUIComponent(plugin.UIComponent{
		ID:      extensions.AppBoardEnvelopeType,
		Type:    plugin.UIComponentTypeEnvelope,
		Name:    "AppBoardView",
		Handler: http.NotFoundHandler(),
	})
	if !errors.Is(err, ErrSurfaceNotHonored) {
		t.Fatalf("err = %v, want ErrSurfaceNotHonored", err)
	}
	if svc.Len() != before {
		t.Error("a handler-bearing component still registered its kind")
	}
}

// TestUnimplementedSurfacesReturnAnError is ADR 0007 §4's "recorded so absence
// is not read as decision", made executable.
//
// RegisterCRUDHandler is the one the ADR names specifically: the agent is the
// owning application's client, so nothing needs it, and implementing it because
// the SDK offers it is the named way this boundary rots. Every one of these
// returns an error rather than nil — a registration that silently succeeds and
// does nothing is the failure mode worth spending an error on.
func TestUnimplementedSurfacesReturnAnError(t *testing.T) {
	t.Parallel()
	host, _ := newHost(t)

	checks := map[string]error{
		"RegisterCRUDHandler":  host.RegisterCRUDHandler("task", nil),
		"RegisterEventHook":    host.RegisterEventHook([]string{"message.sending"}, nil),
		"RegisterConfigSchema": host.RegisterConfigSchema(nil),
		"RegisterConnector":    host.RegisterConnector("webhook", nil),
		"RegisterProvider":     host.RegisterProvider("anthropic", nil),
		"RegisterCLIAdapter":   host.RegisterCLIAdapter("pty", nil),
		"SetConfig":            host.SetConfig("k", "v"),
	}
	for name, err := range checks {
		if !errors.Is(err, ErrSurfaceNotHonored) {
			t.Errorf("%s returned %v, want ErrSurfaceNotHonored", name, err)
		}
	}
	if _, err := host.GetService("db"); !errors.Is(err, ErrSurfaceNotHonored) {
		t.Errorf("GetService returned %v, want ErrSurfaceNotHonored", err)
	}
	if _, err := host.GetConfig("k"); !errors.Is(err, ErrSurfaceNotHonored) {
		t.Errorf("GetConfig returned %v, want ErrSurfaceNotHonored", err)
	}
}

// TestLoadFailureLeavesNoTrace. A plugin whose Load errors must not stay in the
// loaded set, or GetPlugin would hand another plugin a dependency that never
// finished loading.
func TestLoadFailureLeavesNoTrace(t *testing.T) {
	t.Parallel()
	host, _ := newHost(t)
	failing := &stubPlugin{
		id: "tangent.plugin.broken",
		component: &plugin.UIComponent{
			ID: "tangent.no-such-kind", Type: plugin.UIComponentTypeEnvelope, Name: "X",
		},
	}
	if err := host.Load(failing); err == nil {
		t.Fatal("Load succeeded for a plugin whose registration is refused")
	}
	if _, found := host.GetPlugin("tangent.plugin.broken"); found {
		t.Error("a plugin that failed to load is still registered on the host")
	}
	if kinds := host.ContributedKinds(); len(kinds) != 0 {
		t.Errorf("ContributedKinds = %v after a failed load", kinds)
	}
}

// TestPartialRegistrationIsNotRolledBack pins the honest behavior of a plugin
// that registers one kind and then fails on the next.
//
// The plugin comes off the host. The kind it already registered does NOT come
// out of the envelope registry, because go-envelopes' registry is boot-time and
// has no removal — faking a rollback would report a registry state that is not
// the one in force. It does not matter in practice, because the only production
// caller is plugins.LoadShipped and a failed load fails the boot; it is pinned
// here so the doc comment saying so cannot quietly stop being true.
func TestPartialRegistrationIsNotRolledBack(t *testing.T) {
	t.Parallel()
	host, svc, kind := newContributingHost(t)
	before := svc.Len()

	err := host.Load(&stubPlugin{
		id: "tangent.plugin.halfway",
		components: []plugin.UIComponent{
			{ID: kind, Type: plugin.UIComponentTypeEnvelope, Name: "AppBoardView"},
			{ID: "tangent.no-such-kind", Type: plugin.UIComponentTypeEnvelope, Name: "GhostView"},
		},
	})
	if err == nil {
		t.Fatal("Load succeeded despite a refused second registration")
	}
	if _, found := host.GetPlugin("tangent.plugin.halfway"); found {
		t.Error("a plugin that failed to load is still on the host")
	}
	if svc.Len() != before+1 {
		t.Errorf("registry size = %d, want %d: the first kind registered and cannot be removed",
			svc.Len(), before+1)
	}
	if _, found := svc.Lookup(kind); !found {
		t.Error("the successfully registered kind was removed; go-envelopes has no removal, " +
			"so this would mean the host is reporting a state it cannot produce")
	}
}

// TestDuplicatePluginIsRefused. Two plugins under one id would make GetPlugin
// answer arbitrarily.
func TestDuplicatePluginIsRefused(t *testing.T) {
	t.Parallel()
	host, _ := newHost(t)
	if err := host.Load(&stubPlugin{id: "tangent.plugin.dup"}); err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if err := host.Load(&stubPlugin{id: "tangent.plugin.dup"}); !errors.Is(err, ErrDuplicatePlugin) {
		t.Fatalf("second Load err = %v, want ErrDuplicatePlugin", err)
	}
}

// TestMissingDependencyIsRefusedAtLoad, so the failure is a boot error naming
// the missing plugin rather than a nil at first use.
func TestMissingDependencyIsRefusedAtLoad(t *testing.T) {
	t.Parallel()
	host, _ := newHost(t)
	err := host.Load(&stubPlugin{id: "tangent.plugin.needy", deps: []string{"tangent.plugin.absent"}})
	if err == nil {
		t.Fatal("Load succeeded with an unmet dependency")
	}
	if _, found := host.GetPlugin("tangent.plugin.needy"); found {
		t.Error("a plugin with an unmet dependency is registered")
	}
}

// TestOneKindHasOneContributor. Two plugins claiming the same kind is a
// packaging mistake worth a clear error, and catching it here means the second
// one fails on its own name rather than on go-envelopes' duplicate check.
func TestOneKindHasOneContributor(t *testing.T) {
	t.Parallel()
	host, _, kind := newContributingHost(t)
	component := plugin.UIComponent{
		ID: kind, Type: plugin.UIComponentTypeEnvelope, Name: "AppBoardView",
	}
	if err := host.Load(&stubPlugin{id: "tangent.plugin.first", component: &component}); err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if err := host.Load(&stubPlugin{id: "tangent.plugin.second", component: &component}); err == nil {
		t.Fatal("a second plugin claimed a kind already contributed")
	}
}

// TestHostRequiresAnEnvelopeService. A host that cannot register a kind cannot
// honor the one surface it implements; failing at construction beats failing at
// the first registration.
func TestHostRequiresAnEnvelopeService(t *testing.T) {
	t.Parallel()
	if _, err := New(context.Background(), nil, nil); err == nil {
		t.Fatal("New accepted a nil envelope service")
	}
}
