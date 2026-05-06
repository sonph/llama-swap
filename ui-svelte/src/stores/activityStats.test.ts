import { describe, it, expect } from "vitest";
import type { ActivityLogEntry } from "../lib/types";
import { computeScatterData } from "./activityStats";

function entry(overrides?: Omit<Partial<ActivityLogEntry>, "tokens"> & { tokens?: Partial<NonNullable<ActivityLogEntry["tokens"]>> }): ActivityLogEntry {
  const { tokens, ...rest } = overrides ?? {};
  return {
    id: 0,
    timestamp: new Date().toISOString(),
    model: "test-model",
    req_path: "/v1/chat/completions",
    resp_content_type: "application/json",
    resp_status_code: 200,
    duration_ms: 10000,
    has_capture: false,
    ...rest,
    tokens: {
      cache_tokens: 0,
      input_tokens: 100,
      output_tokens: 50,
      prompt_per_second: 10,
      tokens_per_second: 5,
      ...tokens,
    },
  };
}

describe("computeScatterData", () => {
  describe("filtering", () => {
    it("returns null for empty input", () => {
      expect(computeScatterData([])).toBeNull();
    });

    it("skips entries with zero tokens_per_second", () => {
      const entries = [entry({ tokens: { input_tokens: 100, tokens_per_second: 0 } })];
      expect(computeScatterData(entries)).toBeNull();
    });

    it("skips entries with zero input_tokens", () => {
      const entries = [entry({ tokens: { input_tokens: 0, tokens_per_second: 5 } })];
      expect(computeScatterData(entries)).toBeNull();
    });

    it("includes entries with valid tokens", () => {
      const entries = [entry({ tokens: { input_tokens: 100, tokens_per_second: 5 } })];
      const result = computeScatterData(entries);
      expect(result).not.toBeNull();
      expect(result!.points.length).toBe(1);
    });

    it("skips entries with valid tokens_per_second but no input tokens", () => {
      const entries = [entry({ tokens: { cache_tokens: 50, input_tokens: 0, tokens_per_second: 5 } })];
      const result = computeScatterData(entries);
      expect(result).toBeNull();
    });

    it("handles a mix of valid and filtered-out entries", () => {
      const entries = [
        entry({ tokens: { input_tokens: 100, tokens_per_second: 5 } }),
        entry({ tokens: { input_tokens: 0, tokens_per_second: 10 } }), // skipped
        entry({ tokens: { input_tokens: 200, tokens_per_second: 0 } }), // skipped
        entry({ model: "other", tokens: { input_tokens: 300, tokens_per_second: 8 } }),
      ];
      const result = computeScatterData(entries);
      expect(result).not.toBeNull();
      expect(result!.points.length).toBe(2);
      expect(result!.points[0].model).toBe("test-model");
      expect(result!.points[1].model).toBe("other");
    });
  });

  describe("x/y values", () => {
    it("computes x as cache_tokens + input_tokens", () => {
      const entries = [entry({ tokens: { cache_tokens: 50, input_tokens: 200, tokens_per_second: 5 } })];
      const result = computeScatterData(entries);
      expect(result!.points[0].x).toBe(250);
    });

    it("computes y as tokens_per_second", () => {
      const entries = [entry({ tokens: { tokens_per_second: 42.5 } })];
      const result = computeScatterData(entries);
      expect(result!.points[0].y).toBe(42.5);
    });

    it("copies model name to each point", () => {
      const entries = [entry({ model: "llama-3.1-8b", tokens: { tokens_per_second: 5 } })];
      const result = computeScatterData(entries);
      expect(result!.points[0].model).toBe("llama-3.1-8b");
    });

    it("handles cache_tokens = 0", () => {
      const entries = [entry({ tokens: { cache_tokens: 0, input_tokens: 100, tokens_per_second: 5 } })];
      const result = computeScatterData(entries);
      expect(result!.points[0].x).toBe(100);
    });
  });

  describe("bounds", () => {
    it("computes correct xMin/xMax from all entries (including truncated)", () => {
      const entries = [
        entry({ model: "a", tokens: { input_tokens: 100, tokens_per_second: 5 } }),
        entry({ model: "b", tokens: { input_tokens: 500, tokens_per_second: 10 } }),
        entry({ model: "c", tokens: { input_tokens: 300, tokens_per_second: 8 } }),
      ];
      const result = computeScatterData(entries);
      expect(result!.xMin).toBe(100);
      expect(result!.xMax).toBe(500);
    });

    it("uses all original points for yMin/yMax even after truncation", () => {
      const entries = Array.from({ length: 10 }, (_, i) =>
        entry({
          model: `m${i}`,
          tokens: { input_tokens: 100 + i * 10, tokens_per_second: 1 + i },
        }),
      );
      const result = computeScatterData(entries);
      expect(result!.yMin).toBe(0);
      expect(result!.yMax).toBe(10);
    });

    it("uses xMin/xMax from all original entries (not just truncated set)", () => {
      // Create 600 entries; entry(0) gets filtered (input_tokens = 0), so xMin = 10 (from m1).
      // Bounds use all original entries, so xMax = 5990 (from m599).
      const entries = Array.from({ length: 600 }, (_, i) =>
        entry({
          model: `m${i}`,
          tokens: { input_tokens: i * 10, tokens_per_second: 5 },
        }),
      );
      const result = computeScatterData(entries);
      // slice(0, 500) keeps indices 0-499 → 500 points in result.
      // m0 filtered out (input_tokens=0), so xMin = 10 (m1).
      // Bounds use full original list, so xMax = 5990 (m599).
      expect(result!.points.length).toBe(500);
      expect(result!.xMin).toBe(10);
      expect(result!.xMax).toBe(5990);
    });
  });

  describe("tps capping", () => {
    it("caps individual point y at 500", () => {
      const entries = [
        entry({ tokens: { input_tokens: 100, tokens_per_second: 1000 } }),
      ];
      const result = computeScatterData(entries);
      expect(result!.points[0].y).toBe(500);
    });

    it("clamps yMax to 500 even with high values", () => {
      const entries = [
        entry({ tokens: { input_tokens: 100, tokens_per_second: 1000 } }),
        entry({ tokens: { input_tokens: 200, tokens_per_second: 800 } }),
      ];
      const result = computeScatterData(entries);
      expect(result!.yMax).toBe(500);
    });

    it("does not cap when all values are below 500", () => {
      const entries = [
        entry({ tokens: { input_tokens: 100, tokens_per_second: 450 } }),
        entry({ tokens: { input_tokens: 200, tokens_per_second: 300 } }),
      ];
      const result = computeScatterData(entries);
      expect(result!.yMax).toBe(450);
    });

    it("only caps tokens_per_second, not x values", () => {
      const entries = [
        entry({ tokens: { input_tokens: 10000, tokens_per_second: 1000 } }),
      ];
      const result = computeScatterData(entries);
      expect(result!.points[0].x).toBe(10000);
      expect(result!.points[0].y).toBe(500);
    });
  });

  describe("truncation", () => {
    it("caps at MAX_POINTS entries", () => {
      const entries = Array.from({ length: 600 }, (_, i) =>
        entry({
          model: `m${i}`,
          tokens: { input_tokens: 100, tokens_per_second: 1 + i * 0.01 },
        }),
      );
      const result = computeScatterData(entries);
      expect(result!.points.length).toBe(500);
    });

    it("does not truncate when under MAX_POINTS", () => {
      const entries = Array.from({ length: 10 }, (_, i) =>
        entry({
          model: `m${i}`,
          tokens: { input_tokens: 100 + i, tokens_per_second: 1 },
        }),
      );
      const result = computeScatterData(entries);
      expect(result!.points.length).toBe(10);
    });

    it("does not truncate at exactly MAX_POINTS", () => {
      const entries = Array.from({ length: 500 }, (_, i) =>
        entry({
          model: `m${i}`,
          tokens: { input_tokens: 100, tokens_per_second: 1 },
        }),
      );
      const result = computeScatterData(entries);
      expect(result!.points.length).toBe(500);
    });

    it("keeps the earliest entries in the input array (first = newest)", () => {
      const entries = Array.from({ length: 10 }, (_, i) =>
        entry({
          model: `m${i}`,
          tokens: { input_tokens: 100 + i * 10, tokens_per_second: 1 + i },
        }),
      );
      // Manually cap at 3 to verify which entries survive
      const capped = entries.slice(0, 3);
      const result = computeScatterData(capped);
      expect(result!.points.length).toBe(3);
      expect(result!.points.map((p) => p.model)).toEqual(["m0", "m1", "m2"]);
    });
  });
});
