# ADR 0011: Additive Envelope Response History Schema

**Status:** PROPOSED. Awaiting Chrispian's acceptance; this record is not an
accepted exception to the frozen contract.

**Date:** 2026-10-02

**Task:** `CW-20261002-0101`

**Baseline reviewed:** Tangent `6bd1af2f38c6522a9215a0a13575377de0200410`;
go-envelopes `v0.4.0` versus
`fb886c3e1b4db413926b29c275c664ba64453112`.

**Amends, if accepted:** [ADR 0003](0003-definition-and-package-ownership.md)
§8 C6 and A4, solely for the output properties enumerated below. Its general
property-for-property freeze continues to apply. The accepted records remain
the historical record; this proposal does not edit them.

## Context

The MCP SDK reflects `sessionGetResult`, including historical go-envelopes
`Response` values, into the advertised `tangent.session_get` output schema.
Upgrading the dependency adds optional fields to that schema even though
Tangent does not populate them. The freeze tests correctly reject the change.
Their header requires a separately accepted ADR for any digest change.

Upstream commit `8827ce41ec220719cb59732f35c35e12308dc327` adds
`Response.Answers` and `Response.Decisions`. Commit
`91d9ea34e2b214cc99fc7d3fb4ebd5a3305b7ad5` adds the support API and is not
the cause of this reflection change. `Envelope` itself is unchanged.

## Proposed decision

Accept the additive output schema expansion below. Refresh the canonical
frozen output digest in its own commit, retaining the assertions and
reflection/composition behavior. The prepared digest commit is contingent on
acceptance; a passing test on the draft branch does not confer approval.

ADR 0003's property-for-property comparison yields this complete difference.
Paths are relative to the `tangent.session_get` output schema.

| Property | Before | After |
| --- | --- | --- |
| `envelopes_history[].response.answers` | Absent | Optional null-or-array. Item object: required `questionId:string`, `value:any`; optional `acceptedSuggestion:null-or-boolean`, `note:string`; `additionalProperties:false`. |
| `envelopes_history[].response.decisions` | Absent | Optional null-or-array. Item object: required `itemId:string`, `action:string`; optional `note:string`, `meta:object` with `additionalProperties:true`; item `additionalProperties:false`. |

Every existing property, type, required entry and `additionalProperties`
setting is unchanged, including the response's required list. Actual MCP SDK
`tools/list` snapshots from the durable session fixture show no input schema
changes and no other advertised tool output schema changes. This evidence
does not cover externally installed plugin tools.

Both upstream fields use `omitempty`. Nil or empty channels are omitted;
the same legacy payload-only response marshals to identical bytes at both
pins. Nonempty channels add their fields as intended. This is an output
schema change, without an Envelope wire-version change or a manifest
contract-digest change. It does not adopt new response behavior in Tangent.

The canonical digest changes from
`sha256:2ebbc9b5efb3272d93070c0bd0ec1fb737966b3225b2816d48a6b2d79604f664`
to `sha256:3e08c98facfe770c638b4ca089a0ec07ff0ba05f541e95f51d8ff3d390755f54`.
This proposal permits exactly this reviewed expansion, not automatic digest
regeneration on future dependency updates. There is no removal requiring the
deprecation and migration provisions of ADR 0001 §11.7.

## Alternatives considered

### Accept the additive fields

Recommended: preserve the shared response type and advertise its actual
shape. Existing payloads remain valid and unchanged. Strict clients may
notice the expanded schema; acceptance makes that contract decision explicit.

### Tangent owns its schema type

Introduce an explicitly owned projection type and convert shared responses
into it, preserving the old advertised schema. This would isolate dependency
changes but add a maintained conversion boundary and a decision about hiding
new channels. It is a separate ownership change, not a digest workaround in
this adoption task.

### Keep the v0.4.0 pin

Preserves the frozen schema without an exception but defers the reviewed
library upgrade and its wire-only catalog migration. This remains the option
if the proposal is rejected; no rejection may be bypassed by changing the
assertion or silently filtering fields.

## Consequences and consumer exposure

Tangent-inbox at `e5d13175478e04974d6d4847dc457248a4fb0174` has
byte-identical session schema composition, historical response storage and
freeze-test source. It still pins v0.4.0. The same expansion and digest failure
are inferred when it upgrades; inventory only was performed, without edits
or execution there. Its adoption must account for this decision explicitly.

The scoped app inventory found no corresponding reflection exposure in
Nanite (which uses its own `ResponseV1`) or Flux (no Go import of its pinned
go-envelopes dependency). This is not a portfolio-wide compatibility claim.

## Open, for Chrispian's review

Accept or reject this exact additive exception to ADR 0003's frozen output
schema. Until acceptance is recorded and a go-envelopes release tag exists,
the adoption PR remains draft and must not merge. After acceptance, record
the approval here and repin to the release tag before merge. No deployment
or service restart is authorized by this record.

## References

- [ADR 0001](0001-lifecycle-boundaries.md) §11.7 — removal requirements
- [ADR 0003](0003-definition-and-package-ownership.md) C6/A4 — advertised
  schema freeze and property-for-property comparison
- `internal/mcp/interaction_packages_internal_test.go` — digest policy
- Torque `CW-20261002-0101`, artifacts 848–855 — before/after schemas,
  field-level differences, actual tools/list snapshots and legacy bytes;
  artifact 856 — investigation and gate results
