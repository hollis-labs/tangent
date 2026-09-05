import { describe, expect, it } from "vitest";

import {
  DEFINITION_DEFS_DIGESTS,
  DEFINITION_NAMED_DEFINITIONS,
  DEFINITION_SOURCE_ENTRIES,
} from "@/generated/envelope-types";
import { HITL_API_DEFINITION_SOURCE } from "./hitl-api";

const HITL_KIND = "tangent.hitl-item";

/** Every digest the manifest pipeline emits is a `sha256:`-prefixed hex string. */
const DIGEST_PATTERN = /^sha256:[0-9a-f]{64}$/;

describe("hitl-api definition drift", () => {
  /**
   * `hitl-api.ts` is hand-written against a wire contract it cannot see. Nothing
   * in the type system connects `HITLOperatorItem` to `HITLItemViewV1`, so a
   * server-side change to the `$defs` bundle — a renamed field, a widened enum,
   * a newly required property — compiles clean on both sides and only surfaces
   * as an operator staring at an inbox row that silently dropped half its
   * content, or a resolution POST the host rejects. ADR 0003 §4.7 accepts the
   * stamp instead of full generation precisely because this assertion exists:
   * it converts an invisible contract divergence into a red build.
   *
   * When this fails, the bundle moved. Read `hitl-api.ts` against the new
   * bundle first. Updating the stamp to make the test green, without that
   * review, is the one response that leaves production broken and CI happy.
   */
  it("stamps the $defs bundle digest it was last reviewed against", () => {
    expect(
      HITL_API_DEFINITION_SOURCE,
      "the tangent.hitl-item $defs bundle changed: review hitl-api.ts against the new bundle, " +
        "fix what the contract changed, and only then update HITL_API_DEFINITION_SOURCE",
    ).toBe(DEFINITION_DEFS_DIGESTS[HITL_KIND]);
  });

  /**
   * The stamp is only meaningful while the contract still declares what it
   * generates. If `DEFINITION_NAMED_DEFINITIONS` for this kind emptied out — a
   * manifest that stopped exporting its `$defs`, or a generator that quietly
   * stopped collecting them — the digest above would keep matching some
   * degenerate bundle and the drift check would pass while defending nothing.
   * A contract that stops naming its code-generation targets (ADR 0003 §4.7)
   * is itself drift, so assert the targets are still there and still named.
   */
  it("keeps naming the $defs entry points it declares as codegen targets", () => {
    const named = DEFINITION_NAMED_DEFINITIONS[HITL_KIND];
    expect(named, `${HITL_KIND} declares no named $defs`).toBeDefined();

    const entries = Object.entries(named);
    expect(entries.length).toBeGreaterThan(0);
    for (const [name, description] of entries) {
      expect(name.length, "a $defs entry point lost its name").toBeGreaterThan(0);
      expect(description.length, `${name} lost its description`).toBeGreaterThan(0);
    }
  });

  /**
   * ADR 0003 §4 makes the manifest, not the generated file, the thing drift is
   * measured against: a stamp is only as trustworthy as the per-kind identity
   * it was derived from. If `tangent.hitl-item` fell out of
   * `DEFINITION_SOURCE_ENTRIES` — dropped from the extension registry, or
   * missed by the dump tool the way §4.3 describes it missing 18 kinds — the
   * inbox would keep shipping against a contract the host no longer resolves,
   * and no other check in this file would notice. Blank digests are the same
   * failure wearing a placeholder.
   */
  it("carries a per-kind manifest identity with both digests populated", () => {
    const entry = DEFINITION_SOURCE_ENTRIES.find((candidate) => candidate.kind === HITL_KIND);
    expect(
      entry,
      `${HITL_KIND} is missing from DEFINITION_SOURCE_ENTRIES — the generator no longer sees it`,
    ).toBeDefined();
    if (!entry) return;

    expect(entry.contractDigest).toMatch(DIGEST_PATTERN);
    expect(entry.manifestDigest).toMatch(DIGEST_PATTERN);
  });
});
