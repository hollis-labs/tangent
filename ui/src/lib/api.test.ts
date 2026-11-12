import { afterEach, expect, it, vi } from "vitest";
import { apiFetch, readApiError } from "./api";

afterEach(() => vi.unstubAllGlobals());

function stubFetch() {
  const fetchMock = vi.fn(async () => new Response("{}"));
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function sentHeaders(fetchMock: ReturnType<typeof stubFetch>): Headers {
  const calls = fetchMock.mock.calls as unknown as [string, RequestInit | undefined][];
  return new Headers(calls[0][1]?.headers);
}

it("marks every unsafe method to a same-origin path", async () => {
  for (const method of ["POST", "PUT", "PATCH", "DELETE", "post"]) {
    const fetchMock = stubFetch();
    await apiFetch("/api/x", { method, headers: { Accept: "application/json" } });
    const headers = sentHeaders(fetchMock);
    expect(headers.get("X-Requested-With")).toBe("tangent");
    expect(headers.get("Accept")).toBe("application/json");
  }
});

it("passes a safe request through untouched", async () => {
  const fetchMock = stubFetch();
  await apiFetch("/api/x");
  expect(fetchMock).toHaveBeenCalledWith("/api/x");
});

it("keeps a plain header bag plain", async () => {
  const fetchMock = stubFetch();
  await apiFetch("/api/x", { method: "POST", headers: { "If-Match": "a" } });
  const calls = fetchMock.mock.calls as unknown as [string, RequestInit][];
  expect(calls[0][1].headers).toEqual({ "If-Match": "a", "X-Requested-With": "tangent" });
});

it("leaves safe methods unmarked", async () => {
  for (const init of [{ method: "GET" }, { method: "HEAD" }]) {
    const fetchMock = stubFetch();
    await apiFetch("/api/x", init);
    expect(sentHeaders(fetchMock).has("X-Requested-With")).toBe(false);
  }
});

it("never marks a request to another origin", async () => {
  for (const url of ["https://example.test/x", "//example.test/x"]) {
    const fetchMock = stubFetch();
    await apiFetch(url, { method: "POST" });
    const calls = fetchMock.mock.calls as unknown as [string, RequestInit | undefined][];
    expect(new Headers(calls[0][1]?.headers).has("X-Requested-With")).toBe(false);
  }
});

it("reads the flat error body", async () => {
  const response = new Response(
    JSON.stringify({ code: "csrf", message: "missing X-Requested-With", details: { a: 1 } }),
    {
      status: 403,
    },
  );
  expect(await readApiError(response)).toEqual({
    status: 403,
    code: "csrf",
    message: "missing X-Requested-With",
    details: { a: 1 },
  });
});

it("falls back to the status when the body is not the flat shape", async () => {
  const error = await readApiError(new Response("<html>bad gateway</html>", { status: 502 }));
  expect(error).toMatchObject({
    status: 502,
    code: "http_error",
    message: "Request failed (HTTP 502).",
  });
});
