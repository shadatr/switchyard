import { describe, expect, it, vi } from "vitest";
import { ApiError, SwitchyardClient, backoffMs, liveUrl, parseLiveMessage, qs } from "../api";

function mockFetch(status: number, body: unknown) {
  return vi.fn(async () => new Response(typeof body === "string" ? body : JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } }));
}

describe("qs", () => {
  it("drops empty values and keeps order", () => {
    expect(qs({ from: 1, to: 2, project: "", model: undefined, status: null, key: "k" })).toBe("?from=1&to=2&key=k");
  });
  it("returns empty string when nothing is set", () => {
    expect(qs({ a: "", b: undefined })).toBe("");
  });
  it("encodes values", () => {
    expect(qs({ model: "a/b c" })).toBe("?model=a%2Fb+c");
  });
});

describe("liveUrl", () => {
  it("maps http to ws and https to wss with the token", () => {
    expect(liveUrl("http://127.0.0.1:8787", "t0k")).toBe("ws://127.0.0.1:8787/admin/live?token=t0k");
    expect(liveUrl("https://gw.example.com/", "a b")).toBe("wss://gw.example.com/admin/live?token=a+b");
  });
});

describe("SwitchyardClient", () => {
  it("sends the bearer token and builds overview params", async () => {
    const f = mockFetch(200, { requests: 1 });
    const c = new SwitchyardClient({ baseUrl: "http://gw/", token: "secret", fetch: f });
    await c.overview({ from: 10, to: 20, project: "p1" });
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("http://gw/admin/api/overview?from=10&to=20&project=p1");
    expect((init.headers as Record<string, string>).Authorization).toBe("Bearer secret");
    expect(init.method).toBe("GET");
    expect(init.body).toBeUndefined();
  });

  it("passes request filters and cursor", async () => {
    const f = mockFetch(200, { data: [], next_cursor: "" });
    const c = new SwitchyardClient({ baseUrl: "http://gw", token: "t", fetch: f });
    await c.requests({ from: 1, to: 2, model: "m", status: "error", key: "k1", cursor: "abc", limit: 50 });
    expect((f.mock.calls[0] as unknown as [string])[0]).toBe("http://gw/admin/api/requests?from=1&to=2&model=m&status=error&key=k1&cursor=abc&limit=50");
  });

  it("serializes PATCH bodies as JSON", async () => {
    const f = mockFetch(200, { id: "k" });
    const c = new SwitchyardClient({ baseUrl: "http://gw", token: "t", fetch: f });
    await c.updateKey("a/b", { clear_rpm_limit: true });
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("http://gw/admin/api/keys/a%2Fb");
    expect(init.method).toBe("PATCH");
    expect(init.body).toBe('{"clear_rpm_limit":true}');
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe("application/json");
  });

  it("maps 401 to unauthorized with the server message", async () => {
    const c = new SwitchyardClient({ baseUrl: "http://gw", token: "bad", fetch: mockFetch(401, { error: { message: "invalid admin token" } }) });
    const err = await c.targets().catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.kind).toBe("unauthorized");
    expect(err.message).toBe("invalid admin token");
  });

  it("maps 503 to unconfigured", async () => {
    const c = new SwitchyardClient({
      baseUrl: "http://gw",
      token: "t",
      fetch: mockFetch(503, { error: { message: "analytics (ClickHouse) is not configured" } }),
    });
    const err = await c.overview({ from: 1, to: 2 }).catch((e) => e);
    expect(err.kind).toBe("unconfigured");
    expect(err.status).toBe(503);
  });

  it("falls back to status text for non-JSON errors", async () => {
    const c = new SwitchyardClient({ baseUrl: "http://gw", token: "t", fetch: mockFetch(500, "boom") });
    const err = await c.keys().catch((e) => e);
    expect(err.kind).toBe("server");
    expect(err.message).toMatch(/^500/);
  });

  it("maps network failures to kind network", async () => {
    const c = new SwitchyardClient({
      baseUrl: "http://gw",
      token: "t",
      fetch: vi.fn(async () => {
        throw new TypeError("Failed to fetch");
      }),
    });
    const err = await c.keys().catch((e) => e);
    expect(err.kind).toBe("network");
    expect(err.status).toBe(0);
  });
});

describe("backoffMs", () => {
  it("grows exponentially within jitter bounds and caps", () => {
    expect(backoffMs(0, 500, 15000, () => 0)).toBe(250);
    expect(backoffMs(0, 500, 15000, () => 1)).toBe(500);
    expect(backoffMs(3, 500, 15000, () => 1)).toBe(4000);
    expect(backoffMs(20, 500, 15000, () => 1)).toBe(15000);
    expect(backoffMs(20, 500, 15000, () => 0)).toBe(7500);
  });
});

describe("parseLiveMessage", () => {
  it("parses a batch", () => {
    const m = parseLiveMessage('{"events":[{"request_id":"r1","status":200}],"skipped":3}');
    expect(m?.events).toHaveLength(1);
    expect(m?.skipped).toBe(3);
  });
  it("defaults skipped and rejects junk", () => {
    expect(parseLiveMessage('{"events":[]}')?.skipped).toBe(0);
    expect(parseLiveMessage("not json")).toBeNull();
    expect(parseLiveMessage('{"foo":1}')).toBeNull();
    expect(parseLiveMessage(new ArrayBuffer(2))).toBeNull();
  });
});
