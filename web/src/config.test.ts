import { describe, expect, it } from "vitest";
import { resolveOrderApiBase } from "./config";

describe("resolveOrderApiBase", () => {
  it("builds the production API base from the runtime API origin", () => {
    expect(resolveOrderApiBase({ apiOrigin: "http://localhost:8000" }, true)).toBe(
      "http://localhost:8000/api/order-management",
    );
  });

  it("normalizes a trailing slash on the runtime API origin", () => {
    expect(resolveOrderApiBase({ apiOrigin: "https://warehouse.example/" }, true)).toBe(
      "https://warehouse.example/api/order-management",
    );
  });

  it("fails loudly when production runtime configuration has no API origin", () => {
    expect(() => resolveOrderApiBase({}, true)).toThrow(
      "window.__WAREHOUSE_CONFIG__.apiOrigin is required in production",
    );
  });

  it("retains the existing standalone API origin in Vite development", () => {
    expect(resolveOrderApiBase({}, false)).toBe("http://localhost:8086");
  });
});
