import { describe, expect, it } from "vitest";
import { featureBySlug, publicFeatures } from "../app/content/features";

describe("public content boundary", () => {
  it("publishes a complete, unique and governed launch feature map", () => {
    const expected = [
      "your-turn", "work", "knowledge", "baseline", "agents", "schedules", "finance", "marketing",
      "integrations", "account-administration", "security", "export-lifecycle"
    ];
    expect(publicFeatures.map((feature) => feature.slug)).toEqual(expected);
    expect(new Set(publicFeatures.map((feature) => feature.slug)).size).toBe(expected.length);
    for (const feature of publicFeatures) {
      expect(featureBySlug(feature.slug)).toBe(feature);
      expect(feature.name.length).toBeGreaterThan(2);
      expect(feature.title.length).toBeGreaterThan(4);
      expect(feature.summary.length).toBeGreaterThan(20);
      expect(feature.purpose.length).toBeGreaterThan(40);
      expect(feature.workflows).toHaveLength(4);
      expect(feature.boundaries).toHaveLength(3);
    }
  });

  it("uses only Catalog package codes and never stores a paid amount in feature copy", () => {
    expect(publicFeatures.filter((feature) => feature.packageCode).map((feature) => feature.packageCode)).toEqual([
      "work", "knowledge", "agents", "finance", "marketing", "integrations"
    ]);
    expect(JSON.stringify(publicFeatures)).not.toMatch(/\$\s*\d/);
  });
});
