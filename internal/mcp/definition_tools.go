package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hollis-labs/tangent/internal/definition"
	"github.com/hollis-labs/tangent/internal/envelope"
)

// Registry diagnostics. ADR 0003 §4.4 requires the manifest digests the running
// host resolved to be exposed, so a client holding generated types can compare
// its @definition-source stamp against the live digest and refuse to submit
// against a definition it was not generated for. §8 C7 requires incompatible,
// quarantined, and unavailable to be distinguishable rather than collapsed into
// one failure.
//
// Every response here is bounded. A definition's schema bundle can be tens of
// kilobytes — tangent.hitl-item's is 38 KB — and a diagnostic that streams
// eighteen of those is a diagnostic nobody runs twice. Listings page, and
// schema bodies are returned only on explicit request and only under a hard
// ceiling, with an honest marker when the body was withheld.

const (
	// definitionListDefaultLimit is the page size when a caller does not ask.
	definitionListDefaultLimit = 25
	// definitionListMaximumLimit caps what a caller may ask for.
	definitionListMaximumLimit = 100
	// definitionSchemaInlineCeilingBytes is the largest schema body returned
	// inline. Above it the digest and size are reported and the body is
	// withheld — the material is still reachable through the package tree and
	// through the pinned binding, so nothing is lost but the transfer.
	definitionSchemaInlineCeilingBytes = 32 * 1024
)

type definitionRegistryListInput struct {
	// PackageID filters to one package (ADR 0003 §5 ownership boundary).
	PackageID string `json:"package_id,omitempty"`
	// State filters to one materialization state: registered, resolved,
	// verified, materialized, available, incompatible, quarantined, or
	// unavailable.
	State string `json:"state,omitempty"`
	Limit int    `json:"limit,omitempty"`
	// Cursor is the `next_cursor` from a previous page: the kind@version to
	// resume after.
	Cursor string `json:"cursor,omitempty"`
}

type definitionGetInput struct {
	Kind    string `json:"kind"`
	Version string `json:"version,omitempty"`
	// IncludeSchemas returns the request and response schema bodies when they
	// fit under the inline ceiling. Off by default.
	IncludeSchemas bool `json:"include_schemas,omitempty"`
}

type definitionRegistryDiagnosticsInput struct{}

// definitionSummary is one row of the registry listing: identity, ownership,
// renderer binding, state, and digests — never schema bodies.
type definitionSummary struct {
	Kind           string `json:"kind"`
	Version        string `json:"version"`
	Revision       int64  `json:"revision"`
	Title          string `json:"title,omitempty"`
	Publisher      string `json:"publisher"`
	PackageID      string `json:"package_id"`
	PackageVersion string `json:"package_version"`
	OwnershipClass string `json:"ownership_class"`
	ResponseKind   string `json:"response_kind"`
	State          string `json:"materialization_state"`
	StateReason    string `json:"state_reason,omitempty"`
	ErrorCode      string `json:"error_code,omitempty"`
	Available      bool   `json:"available"`
	RendererID     string `json:"renderer_id"`
	RendererClass  string `json:"renderer_class"`
	// RendererTrustClass is the class Tangent *granted*. A requested class the
	// trust evidence did not support is quarantined rather than downgraded, so
	// this field is empty exactly when the evidence gate refused.
	RendererTrustClass string `json:"renderer_trust_class"`
	// RendererIsolation is where that class runs the renderer, and it is the
	// field that makes a trust class mean something to a reader: `main-origin`
	// carries Tangent's own authority, `sandboxed-frame` carries none.
	RendererIsolation string `json:"renderer_isolation"`
	// AmbientHostAuthority is the same fact as a boolean, so a caller checking
	// "does untrusted code run with host authority here" does not have to know
	// the isolation vocabulary to answer it.
	AmbientHostAuthority bool   `json:"renderer_ambient_host_authority"`
	TrustAssurance       string `json:"trust_assurance"`
	ManifestDigest       string `json:"manifest_digest"`
	ContractDigest       string `json:"contract_digest"`
}

type definitionRegistryListResult struct {
	Definitions []definitionSummary `json:"definitions"`
	// Total is the number matching the filter, so a caller knows whether the
	// page it holds is the whole answer.
	Total int `json:"total"`
	// NextCursor is empty on the last page.
	NextCursor string `json:"next_cursor,omitempty"`
	// SourceDigest is the whole registry's @definition-source stamp. A client
	// compares it against the stamp on its generated types.
	SourceDigest string `json:"definition_source_digest"`
	HostVersion  string `json:"host_version"`
}

// schemaProjection describes one schema without necessarily carrying it.
type schemaProjection struct {
	// Present is false for a definition carrying
	// compatibility_response_schema: absent.
	Present bool   `json:"present"`
	Digest  string `json:"digest,omitempty"`
	Bytes   int    `json:"bytes"`
	// Body is populated only when include_schemas was requested and the
	// document fits under the inline ceiling.
	Body json.RawMessage `json:"body,omitempty"`
	// OmittedReason explains a withheld body rather than leaving the caller to
	// guess whether the schema exists.
	OmittedReason string `json:"omitted_reason,omitempty"`
}

type definitionDetail struct {
	definitionSummary
	Description string `json:"description,omitempty"`

	CompatibleHostVersions      string   `json:"compatible_host_versions"`
	CompatibleProtocolVersions  string   `json:"compatible_protocol_versions"`
	CompatibleSDKVersions       string   `json:"compatible_sdk_versions,omitempty"`
	CompatibilityClass          string   `json:"compatibility_class"`
	CompatibilityResponseSchema string   `json:"compatibility_response_schema,omitempty"`
	Supersedes                  []string `json:"supersedes,omitempty"`

	RendererEntry       string `json:"renderer_entry,omitempty"`
	RendererAssetDigest string `json:"renderer_asset_digest,omitempty"`
	// FallbackRendererID is populated only when the declared fallback
	// preserves meaning — the only fallback Tangent will ever use.
	FallbackRendererID  string `json:"fallback_renderer_id,omitempty"`
	FallbackDegradation string `json:"fallback_degradation,omitempty"`

	// RequiredCapabilities and GrantedCapabilities are host-mediated *effect*
	// capabilities (ADR 0003 §2.5): what the renderer may cause the host to
	// do. They are a different namespace from the object-access capabilities
	// in ADR 0004 §2, which govern who may perform an operation on a surface
	// or interaction, and the two never substitute for one another.
	RequiredCapabilities []definition.Capability `json:"required_renderer_effect_capabilities"`
	GrantedCapabilities  []definition.Capability `json:"granted_renderer_effect_capabilities"`
	DeniedCapabilities   []definition.Capability `json:"denied_renderer_effect_capabilities,omitempty"`
	// TrustDeniedCapabilities is what the *trust class* refuses, which is a
	// different fact from host policy declining to grant something and is
	// reported separately so an operator is not told to widen a policy that
	// would not help. The ceiling is evaluated before the grant, so nothing
	// here can be recovered by an operator.
	TrustDeniedCapabilities []definition.Capability `json:"trust_denied_renderer_effect_capabilities,omitempty"`
	// PermittedCapabilities is the whole ceiling for this renderer's trust
	// class: what a manifest in that class may ever declare, whether or not
	// this one does.
	PermittedCapabilities []string `json:"trust_class_permitted_capabilities,omitempty"`

	DraftCustody string `json:"draft_custody"`
	// RetentionClass is nil when the publisher authored nothing, which is the
	// deliberate state for every shipped kind: ADR 0002 §4's host default then
	// governs, and authoring a value here would loosen it.
	RetentionClass              *definition.RetentionClass `json:"retention_class,omitempty"`
	RetentionClassAuthored      bool                       `json:"retention_class_authored"`
	SensitivityDefault          string                     `json:"sensitivity_default"`
	InlinePayloadLimitBytes     int64                      `json:"inline_payload_limit_bytes"`
	EffectiveInlineLimitBytes   int64                      `json:"effective_inline_payload_limit_bytes"`
	ClientPersistenceProhibited bool                       `json:"client_persistence_prohibited"`

	TrustSourceLocator string `json:"trust_source_locator,omitempty"`
	QuarantineReason   string `json:"quarantine_reason,omitempty"`
	VerifiedAt         string `json:"verified_at"`

	TelemetryEmits        []string `json:"telemetry_emits,omitempty"`
	TelemetryRedactFields []string `json:"telemetry_redact_fields,omitempty"`
	TelemetryOptIn        bool     `json:"telemetry_opt_in"`

	NamedDefinitions map[string]string `json:"named_definitions,omitempty"`

	RequestSchema  schemaProjection `json:"request_schema"`
	ResponseSchema schemaProjection `json:"response_schema"`
	ErrorSchema    schemaProjection `json:"error_schema"`

	SchemaIdentity string `json:"request_schema_identity,omitempty"`
}

type definitionRegistryDiagnosticsResult struct {
	HostVersion     string `json:"host_version"`
	ProtocolVersion string `json:"protocol_version"`
	// SourceDigest is the registry-wide @definition-source stamp.
	SourceDigest string `json:"definition_source_digest"`
	// RegisteredKinds counts every kind the upstream registry holds, core
	// included. ManagedDefinitions counts the subset carrying a manifest.
	RegisteredKinds    int `json:"registered_kinds"`
	ManagedDefinitions int `json:"managed_definitions"`
	// StateCounts is keyed by materialization state, so an operator can see at
	// a glance that nothing is quarantined without reading every entry.
	StateCounts map[string]int `json:"materialization_state_counts"`
	// Unservable names every definition that is not available, with its
	// reason. Bounded by the number of definitions, which is bounded by the
	// registry.
	Unservable []definitionSummary `json:"unservable"`
	// RetainedDefinitions is how many pinned definitions have durable
	// material — the replay guarantee's coverage. -1 when no material store is
	// installed, which is a distinct answer from zero.
	RetainedDefinitions int64 `json:"retained_definitions"`
	// PackageCounts is the ADR 0003 §5 ownership split, counted.
	PackageCounts map[string]int `json:"package_counts"`
	// CapabilityRequests lists every host-mediated effect capability any
	// shipped definition asks for. Empty in v0.x; CW-20260825-0077 is what
	// gives this content, and CW-20260825-0066 extends this result with
	// readiness and capability health.
	CapabilityRequests []string `json:"renderer_effect_capability_requests"`
}

func (s *Server) registerDefinitionTools() error {
	if err := addInteractionTool(s, "tangent.definition_registry_list",
		"List the versioned interaction-definition registry with each definition's materialization state, "+
			"ownership, renderer binding, and manifest digests. Paged and payload-bounded; schema bodies are "+
			"never included.",
		s.handleDefinitionRegistryList); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.definition_get",
		"Get one interaction definition manifest's full inspectable projection: contract, renderer, "+
			"capabilities, persistence, trust, compatibility, and derived digests. Schema bodies are returned "+
			"only with include_schemas and only under a size ceiling.",
		s.handleDefinitionGet); err != nil {
		return err
	}
	if err := addInteractionTool(s, "tangent.definition_registry_diagnostics",
		"Report registry health: the definition source digest, counts by materialization state, every "+
			"unservable definition with its reason, retained-material coverage, and the host-mediated "+
			"capabilities definitions request.",
		s.handleDefinitionRegistryDiagnostics); err != nil {
		return err
	}
	return nil
}

func (s *Server) handleDefinitionRegistryList(
	_ context.Context,
	_ *mcpsdk.CallToolRequest,
	input definitionRegistryListInput,
) (*mcpsdk.CallToolResult, any, error) {
	if input.State != "" && !knownMaterializationState(input.State) {
		return toolErrorResult("invalid_request",
			fmt.Sprintf("unknown materialization state %q", input.State)), nil, nil
	}
	limit := input.Limit
	switch {
	case limit <= 0:
		limit = definitionListDefaultLimit
	case limit > definitionListMaximumLimit:
		limit = definitionListMaximumLimit
	}

	matched := make([]definitionSummary, 0, len(s.envSvc.MaterializedDefinitions()))
	for _, materialized := range s.envSvc.MaterializedDefinitions() {
		if input.PackageID != "" && materialized.Manifest.PackageID != input.PackageID {
			continue
		}
		if input.State != "" && string(materialized.State) != input.State {
			continue
		}
		matched = append(matched, summarize(materialized))
	}

	page := matched
	if input.Cursor != "" {
		page = nil
		for index, entry := range matched {
			if definitionCursor(entry) > input.Cursor {
				page = matched[index:]
				break
			}
		}
	}
	result := definitionRegistryListResult{Total: len(matched), HostVersion: HostVersion}
	if len(page) > limit {
		result.NextCursor = definitionCursor(page[limit-1])
		page = page[:limit]
	}
	result.Definitions = page
	digest, err := s.envSvc.DefinitionSourceDigest()
	if err != nil {
		return toolErrorResult("definition_unavailable", err.Error()), nil, nil
	}
	result.SourceDigest = digest
	return nil, result, nil
}

func (s *Server) handleDefinitionGet(
	_ context.Context,
	_ *mcpsdk.CallToolRequest,
	input definitionGetInput,
) (*mcpsdk.CallToolResult, any, error) {
	if input.Kind == "" {
		return toolErrorResult("invalid_request", "kind is required"), nil, nil
	}
	version := input.Version
	if version == "" {
		spec, ok := s.envSvc.Lookup(input.Kind)
		if !ok {
			return toolErrorResult("definition_not_found", input.Kind+" is not registered"), nil, nil
		}
		version = spec.Version
	}
	material, ok := s.envSvc.LookupDefinitionMaterialVersion(input.Kind, version)
	if !ok || material.Definition == nil {
		return toolErrorResult("definition_not_found",
			fmt.Sprintf("%s@%s has no retained manifest; go-envelopes core definitions carry no source bytes",
				input.Kind, version)), nil, nil
	}
	materialized := *material.Definition
	manifest := materialized.Manifest

	detail := definitionDetail{
		definitionSummary: summarize(materialized),
		Description:       manifest.Description,

		CompatibleHostVersions:      manifest.CompatibleHostVersions,
		CompatibleProtocolVersions:  manifest.CompatibleProtocolVersions,
		CompatibleSDKVersions:       manifest.CompatibleSDKVersions,
		CompatibilityClass:          string(manifest.CompatibilityClass),
		CompatibilityResponseSchema: string(manifest.CompatibilityResponseSchema),
		Supersedes:                  manifest.Supersedes,

		RendererEntry:       manifest.Renderer.Entry,
		RendererAssetDigest: manifest.Renderer.AssetDigest,

		RequiredCapabilities:    nonNilCapabilities(manifest.RequiredCapabilities),
		GrantedCapabilities:     nonNilCapabilities(materialized.GrantedCapabilities),
		DeniedCapabilities:      materialized.DeniedCapabilities,
		TrustDeniedCapabilities: materialized.TrustDeniedCapabilities,

		DraftCustody:                string(manifest.DraftCustody),
		RetentionClass:              manifest.RetentionClass,
		RetentionClassAuthored:      manifest.RetentionClass != nil,
		SensitivityDefault:          string(manifest.SensitivityDefault),
		InlinePayloadLimitBytes:     manifest.InlinePayloadLimitBytes,
		EffectiveInlineLimitBytes:   materialized.EffectiveInlineLimitBytes,
		ClientPersistenceProhibited: manifest.ClientPersistenceProhibited,

		TrustSourceLocator: materialized.SourceLocator,
		QuarantineReason:   materialized.QuarantineReason,
		VerifiedAt:         materialized.VerifiedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),

		TelemetryEmits:        manifest.Telemetry.Emits,
		TelemetryRedactFields: manifest.Telemetry.RedactFields,
		TelemetryOptIn:        manifest.Telemetry.TelemetryOptIn(),

		NamedDefinitions: manifest.NamedDefinitions,
		SchemaIdentity:   material.SchemaIdentity,
	}
	if fallback, ok := materialized.SafeFallback(); ok {
		detail.FallbackRendererID = fallback.RendererID
		detail.FallbackDegradation = string(fallback.Degradation)
	}
	if profile, ok := definition.TrustProfileFor(manifest.Renderer.TrustClass); ok {
		detail.PermittedCapabilities = profile.Capabilities
	}
	detail.RequestSchema = projectSchema(
		material.RequestSchema, materialized.Derived.RequestSchemaDigest, input.IncludeSchemas, true)
	detail.ResponseSchema = projectSchema(
		material.ResponseSchema, materialized.Derived.ResponseSchemaDigest, input.IncludeSchemas,
		manifest.CompatibilityResponseSchema != definition.ResponseSchemaAbsent)
	detail.ErrorSchema = projectSchema(
		material.ErrorSchema, materialized.Derived.ErrorSchemaDigest, input.IncludeSchemas,
		manifest.ErrorSchemaRef != "")
	return nil, detail, nil
}

func (s *Server) handleDefinitionRegistryDiagnostics(
	ctx context.Context,
	_ *mcpsdk.CallToolRequest,
	_ definitionRegistryDiagnosticsInput,
) (*mcpsdk.CallToolResult, any, error) {
	materialized := s.envSvc.MaterializedDefinitions()
	result := definitionRegistryDiagnosticsResult{
		HostVersion:         HostVersion,
		ProtocolVersion:     envelope.ProtocolVersion,
		RegisteredKinds:     s.envSvc.Len(),
		ManagedDefinitions:  len(materialized),
		StateCounts:         map[string]int{},
		PackageCounts:       map[string]int{},
		Unservable:          []definitionSummary{},
		CapabilityRequests:  []string{},
		RetainedDefinitions: -1,
	}
	capabilities := map[string]bool{}
	for _, item := range materialized {
		result.StateCounts[string(item.State)]++
		result.PackageCounts[item.Manifest.PackageID]++
		if !item.State.Servable() {
			result.Unservable = append(result.Unservable, summarize(item))
		}
		for _, capability := range item.Manifest.RequiredCapabilities {
			capabilities[capability.ID] = true
		}
	}
	for id := range capabilities {
		result.CapabilityRequests = append(result.CapabilityRequests, id)
	}
	sort.Strings(result.CapabilityRequests)

	digest, err := s.envSvc.DefinitionSourceDigest()
	if err != nil {
		return toolErrorResult("definition_unavailable", err.Error()), nil, nil
	}
	result.SourceDigest = digest

	if s.interactions != nil {
		count, countErr := s.interactions.RetainedDefinitionCount(ctx)
		if countErr == nil {
			result.RetainedDefinitions = count
		}
	}
	return nil, result, nil
}

func summarize(materialized definition.Materialized) definitionSummary {
	manifest := materialized.Manifest
	return definitionSummary{
		Kind: manifest.Kind, Version: manifest.Version, Revision: manifest.Revision,
		Title: manifest.Title, Publisher: manifest.Publisher,
		PackageID: manifest.PackageID, PackageVersion: manifest.PackageVersion,
		OwnershipClass: string(manifest.OwnershipClass), ResponseKind: manifest.ResponseKind,
		State: string(materialized.State), StateReason: materialized.StateReason,
		ErrorCode: materialized.ErrorCode, Available: materialized.State.Servable(),
		RendererID: manifest.Renderer.ID, RendererClass: string(manifest.Renderer.Class),
		RendererTrustClass:   string(materialized.TrustClass),
		RendererIsolation:    string(materialized.Isolation),
		AmbientHostAuthority: materialized.Isolation.AmbientHostAuthority(),
		TrustAssurance:       string(materialized.Assurance),
		ManifestDigest:       materialized.Derived.ManifestDigest,
		ContractDigest:       materialized.Derived.ContractDigest,
	}
}

// projectSchema describes one schema and includes its body only when asked and
// only when it fits. An omitted body always says why, so "no schema" and "too
// big to send" never look alike.
func projectSchema(body []byte, digest string, include bool, present bool) schemaProjection {
	projection := schemaProjection{Present: present && len(body) > 0, Digest: digest, Bytes: len(body)}
	if !projection.Present {
		if !present {
			projection.OmittedReason = "the definition declares no schema for this role"
		}
		return projection
	}
	if !include {
		projection.OmittedReason = "pass include_schemas to receive the body"
		return projection
	}
	if len(body) > definitionSchemaInlineCeilingBytes {
		projection.OmittedReason = fmt.Sprintf(
			"schema is %d bytes, above the %d-byte inline ceiling; read it from the package tree",
			len(body), definitionSchemaInlineCeilingBytes)
		return projection
	}
	projection.Body = json.RawMessage(body)
	return projection
}

func nonNilCapabilities(capabilities []definition.Capability) []definition.Capability {
	if capabilities == nil {
		return []definition.Capability{}
	}
	return capabilities
}

func definitionCursor(summary definitionSummary) string {
	return summary.Kind + "@" + summary.Version
}

func knownMaterializationState(state string) bool {
	switch definition.State(state) {
	case definition.StateRegistered, definition.StateResolved, definition.StateVerified,
		definition.StateMaterialized, definition.StateAvailable, definition.StateIncompatible,
		definition.StateQuarantined, definition.StateUnavailable:
		return true
	}
	return false
}
