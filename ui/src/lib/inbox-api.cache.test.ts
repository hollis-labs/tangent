import { afterEach, describe, expect, it, vi } from "vitest";
import { fetchInbox, type InboxCache, type InboxEntry } from "./inbox-api";

afterEach(() => vi.restoreAllMocks());

const entry = { sequence: 1 } as InboxEntry;

describe("fetchInbox conditional reads", () => {
  it("sends If-None-Match and returns the same array on 304", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ items: [entry] }), {
          status: 200,
          headers: { ETag: '"v1"' },
        }),
      )
      .mockResolvedValueOnce(new Response(null, { status: 304, headers: { ETag: '"v1"' } }));
    const cache: InboxCache = {};
    const first = await fetchInbox(undefined, cache);
    const second = await fetchInbox(undefined, cache);
    expect(second).toBe(first);
    const headers = (fetchMock.mock.calls[1][1] as RequestInit).headers as Record<string, string>;
    expect(headers["If-None-Match"]).toBe('"v1"');
    expect((fetchMock.mock.calls[0][1] as RequestInit).headers).not.toHaveProperty("If-None-Match");
  });

  it("replaces the cache when the list changes", async () => {
    vi.spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ items: [entry] }), { headers: { ETag: '"v1"' } }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ items: [entry, entry] }), { headers: { ETag: '"v2"' } }),
      );
    const cache: InboxCache = {};
    await fetchInbox(undefined, cache);
    const next = await fetchInbox(undefined, cache);
    expect(next).toHaveLength(2);
    expect(cache.etag).toBe('"v2"');
  });
});
