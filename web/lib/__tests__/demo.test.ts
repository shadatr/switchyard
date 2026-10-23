import { describe, expect, it } from "vitest";
import { nearestPreset } from "../demo";

describe("nearestPreset", () => {
  it("maps window lengths to the closest recorded range", () => {
    expect(nearestPreset(15 * 60e3)).toBe("15m");
    expect(nearestPreset(3600e3)).toBe("1h");
    expect(nearestPreset(5 * 3600e3)).toBe("6h");
    expect(nearestPreset(30 * 3600e3)).toBe("24h");
    expect(nearestPreset(30 * 24 * 3600e3)).toBe("7d");
    expect(nearestPreset(60e3)).toBe("15m");
  });
});

describe("demoFetch routing", () => {
  it("serves admin paths behind the 'demo' base URL", async () => {
    const files: Record<string, unknown> = {
      "/demo/meta.json": { captured_at: Date.now(), projects: [] },
      "/demo/1h/all/overview.json": { requests: 42 },
    };
    const realFetch = globalThis.fetch;
    globalThis.fetch = (async (u: string) =>
      files[u] ? new Response(JSON.stringify(files[u])) : new Response("{}", { status: 404 })) as typeof fetch;
    try {
      const { demoFetch } = await import("../demo");
      const to = Date.now();
      const res = await demoFetch(`demo/admin/api/overview?from=${to - 3600e3}&to=${to}`);
      expect(res.status).toBe(200);
      expect((await res.json()).requests).toBe(42);
    } finally {
      globalThis.fetch = realFetch;
    }
  });
});
