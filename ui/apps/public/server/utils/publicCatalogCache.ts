import type {
  AIComplexity,
  AIComplexityRate,
  AITokenBundle,
  AITokenRenewalGrant,
  CatalogFeaturePackage,
  CatalogLimitDefinition,
  CatalogOffer,
  CatalogPackageCode,
  CatalogPackageMode,
  CatalogPlan,
  PublicCatalog
} from "@spyglass/api";

type UnknownRecord = Record<string, unknown>;

const packageCodes = new Set<CatalogPackageCode>(["knowledge", "work", "agents", "finance", "marketing", "integrations"]);
const packageModes = new Set<CatalogPackageMode>(["enabled", "read_only", "suspended"]);
const limitCombinations = new Set<CatalogLimitDefinition["combine"]>(["replace", "add", "maximum", "minimum"]);
const billingIntervals = new Set<CatalogOffer["billing_interval"]>(["none", "month", "year"]);
const complexities = new Set<AIComplexity>(["simple", "efficient", "balanced", "thorough", "advanced"]);
const forbiddenKey = /(stripe|provider|price_id|product_id|customer_id|subscription_id)/i;
const isoTimestamp = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/;

export class PublicCatalogValidationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "PublicCatalogValidationError";
  }
}

export class PublicCatalogUnavailableError extends Error {
  constructor(options?: ErrorOptions) {
    super("No recently verified public Catalog is available", options);
    this.name = "PublicCatalogUnavailableError";
  }
}

function fail(message: string): never {
  throw new PublicCatalogValidationError(message);
}

function asRecord(value: unknown, label: string): UnknownRecord {
  if (!value || typeof value !== "object" || Array.isArray(value)) fail(`${label} must be an object`);
  return value as UnknownRecord;
}

function asArray(value: unknown, label: string): ReadonlyArray<unknown> {
  if (!Array.isArray(value)) fail(`${label} must be an array`);
  return value;
}

function asString(value: unknown, label: string): string {
  if (typeof value !== "string" || value.trim() === "") fail(`${label} must be a non-empty string`);
  return value;
}

function asInteger(value: unknown, label: string, minimum = 0): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum) fail(`${label} must be an integer greater than or equal to ${minimum}`);
  return value as number;
}

function asDate(value: unknown, label: string): string {
  const text = asString(value, label);
  if (!isoTimestamp.test(text) || !Number.isFinite(Date.parse(text))) fail(`${label} must be an ISO timestamp`);
  return text;
}

function asPackageCode(value: unknown, label: string): CatalogPackageCode {
  if (typeof value !== "string" || !packageCodes.has(value as CatalogPackageCode)) fail(`${label} is not a published package code`);
  return value as CatalogPackageCode;
}

function rejectForbiddenKeys(value: unknown): void {
  if (Array.isArray(value)) {
    value.forEach(rejectForbiddenKeys);
    return;
  }
  if (!value || typeof value !== "object") return;
  for (const [key, child] of Object.entries(value as UnknownRecord)) {
    if (forbiddenKey.test(key)) fail("Catalog contains a provider-specific field");
    rejectForbiddenKeys(child);
  }
}

function copyPackage(value: unknown, index: number): CatalogFeaturePackage {
  const item = asRecord(value, `packages[${index}]`);
  const code = asPackageCode(item.code, `packages[${index}].code`);
  const defaultLimits = item.default_limits === undefined ? undefined : Object.fromEntries(
    Object.entries(asRecord(item.default_limits, `packages[${index}].default_limits`)).map(([key, amount]) => [key, asInteger(amount, `packages[${index}].default_limits.${key}`)])
  );
  const dependencies = item.dependencies === undefined ? undefined : asArray(item.dependencies, `packages[${index}].dependencies`).map(
    (dependency, dependencyIndex) => asPackageCode(dependency, `packages[${index}].dependencies[${dependencyIndex}]`)
  );
  return {
    code,
    ...(defaultLimits ? { default_limits: defaultLimits } : {}),
    ...(dependencies ? { dependencies } : {}),
    description: asString(item.description, `packages[${index}].description`),
    features: asArray(item.features, `packages[${index}].features`).map((feature, featureIndex) => asString(feature, `packages[${index}].features[${featureIndex}]`)),
    name: asString(item.name, `packages[${index}].name`),
    version: asInteger(item.version, `packages[${index}].version`, 1)
  };
}

function copyLimit(value: unknown, index: number): CatalogLimitDefinition {
  const item = asRecord(value, `limits[${index}]`);
  const combine = asString(item.combine, `limits[${index}].combine`);
  if (!limitCombinations.has(combine as CatalogLimitDefinition["combine"])) fail(`limits[${index}].combine is invalid`);
  if (item.kind !== "capacity") fail(`limits[${index}].kind is invalid`);
  return {
    code: asString(item.code, `limits[${index}].code`),
    combine: combine as CatalogLimitDefinition["combine"],
    kind: "capacity",
    name: asString(item.name, `limits[${index}].name`),
    package_code: asPackageCode(item.package_code, `limits[${index}].package_code`),
    ...(item.reservation_ttl_seconds === undefined ? {} : { reservation_ttl_seconds: asInteger(item.reservation_ttl_seconds, `limits[${index}].reservation_ttl_seconds`, 1) }),
    unit: asString(item.unit, `limits[${index}].unit`)
  };
}

function copyPlan(value: unknown, index: number): CatalogPlan {
  const item = asRecord(value, `plans[${index}]`);
  const modes = Object.fromEntries(Object.entries(asRecord(item.packages, `plans[${index}].packages`)).map(([code, mode]) => {
    asPackageCode(code, `plans[${index}].packages.${code}`);
    if (typeof mode !== "string" || !packageModes.has(mode as CatalogPackageMode)) fail(`plans[${index}].packages.${code} is invalid`);
    return [code, mode as CatalogPackageMode];
  }));
  return {
    code: asString(item.code, `plans[${index}].code`),
    description: asString(item.description, `plans[${index}].description`),
    name: asString(item.name, `plans[${index}].name`),
    packages: modes,
    version: asInteger(item.version, `plans[${index}].version`, 1)
  };
}

function copyOffer(value: unknown, index: number): CatalogOffer {
  const item = asRecord(value, `offers[${index}]`);
  const interval = asString(item.billing_interval, `offers[${index}].billing_interval`);
  if (!billingIntervals.has(interval as CatalogOffer["billing_interval"])) fail(`offers[${index}].billing_interval is invalid`);
  const currency = asString(item.currency, `offers[${index}].currency`);
  if (!/^[A-Z]{3}$/.test(currency)) fail(`offers[${index}].currency is invalid`);
  return {
    amount_minor: asInteger(item.amount_minor, `offers[${index}].amount_minor`),
    billing_interval: interval as CatalogOffer["billing_interval"],
    code: asString(item.code, `offers[${index}].code`),
    currency,
    effective_from: asDate(item.effective_from, `offers[${index}].effective_from`),
    plan_code: asString(item.plan_code, `offers[${index}].plan_code`),
    plan_version: asInteger(item.plan_version, `offers[${index}].plan_version`, 1)
  };
}

function copyRenewalGrant(value: unknown): AITokenRenewalGrant {
  const item = asRecord(value, "ai_token_renewal_grant");
  return { code: asString(item.code, "ai_token_renewal_grant.code"), version: asInteger(item.version, "ai_token_renewal_grant.version", 1), quantity: asInteger(item.quantity, "ai_token_renewal_grant.quantity", 1), disclosure: asString(item.disclosure, "ai_token_renewal_grant.disclosure") };
}

function copyTokenBundle(value: unknown, index: number): AITokenBundle {
  const item = asRecord(value, `ai_token_bundles[${index}]`);
  if (item.currency !== "USD") fail(`ai_token_bundles[${index}].currency is invalid`);
  return { code: asString(item.code, `ai_token_bundles[${index}].code`), version: asInteger(item.version, `ai_token_bundles[${index}].version`, 1), quantity: asInteger(item.quantity, `ai_token_bundles[${index}].quantity`, 1), currency: "USD", amount_minor: asInteger(item.amount_minor, `ai_token_bundles[${index}].amount_minor`, 1), effective_from: asDate(item.effective_from, `ai_token_bundles[${index}].effective_from`), disclosure: asString(item.disclosure, `ai_token_bundles[${index}].disclosure`) };
}

function copyComplexityRate(value: unknown, index: number): AIComplexityRate {
  const item = asRecord(value, `ai_complexity_rates[${index}]`);
  const complexity = asString(item.complexity, `ai_complexity_rates[${index}].complexity`) as AIComplexity;
  if (!complexities.has(complexity)) fail(`ai_complexity_rates[${index}].complexity is invalid`);
  return { code: asString(item.code, `ai_complexity_rates[${index}].code`), version: asInteger(item.version, `ai_complexity_rates[${index}].version`, 1), complexity, input_per_thousand: asInteger(item.input_per_thousand, `ai_complexity_rates[${index}].input_per_thousand`, 1), cached_input_per_thousand: asInteger(item.cached_input_per_thousand, `ai_complexity_rates[${index}].cached_input_per_thousand`, 1), output_per_thousand: asInteger(item.output_per_thousand, `ai_complexity_rates[${index}].output_per_thousand`, 1), tool_invocation: asInteger(item.tool_invocation, `ai_complexity_rates[${index}].tool_invocation`), minimum_charge: asInteger(item.minimum_charge, `ai_complexity_rates[${index}].minimum_charge`, 1), maximum_reservation: asInteger(item.maximum_reservation, `ai_complexity_rates[${index}].maximum_reservation`, 1), estimated_minimum: asInteger(item.estimated_minimum, `ai_complexity_rates[${index}].estimated_minimum`, 1), estimated_maximum: asInteger(item.estimated_maximum, `ai_complexity_rates[${index}].estimated_maximum`, 1) };
}

function assertUnique(values: ReadonlyArray<string>, label: string): void {
  if (new Set(values).size !== values.length) fail(`${label} contains duplicate identities`);
}

export function normalizePublicCatalog(value: unknown): PublicCatalog {
  rejectForbiddenKeys(value);
  const source = asRecord(value, "Catalog");
  const packages = asArray(source.packages, "packages").map(copyPackage);
  const limits = asArray(source.limits, "limits").map(copyLimit);
  const plans = asArray(source.plans, "plans").map(copyPlan);
  const offers = asArray(source.offers, "offers").map(copyOffer);
  const aiTokenRenewalGrant = copyRenewalGrant(source.ai_token_renewal_grant);
  const aiTokenBundles = asArray(source.ai_token_bundles, "ai_token_bundles").map(copyTokenBundle);
  const aiComplexityRates = asArray(source.ai_complexity_rates, "ai_complexity_rates").map(copyComplexityRate);
  assertUnique(packages.map((item) => item.code), "packages");
  assertUnique(limits.map((item) => item.code), "limits");
  assertUnique(plans.map((item) => `${item.code}:${item.version}`), "plans");
  assertUnique(offers.map((item) => item.code), "offers");
  assertUnique(aiTokenBundles.map((item) => item.code), "ai_token_bundles");
  assertUnique(aiComplexityRates.map((item) => item.complexity), "ai_complexity_rates");
  if (aiComplexityRates.length !== 5 || complexities.size !== new Set(aiComplexityRates.map((item) => item.complexity)).size) fail("ai_complexity_rates must publish all five classes");

  const publishedPackages = new Set(packages.map((item) => item.code));
  const publishedPlans = new Set(plans.map((item) => `${item.code}:${item.version}`));
  for (const item of packages) {
    if (item.dependencies?.some((dependency) => !publishedPackages.has(dependency))) fail("Package dependency is not published");
  }
  for (const item of plans) {
    if (Object.keys(item.packages).some((code) => !publishedPackages.has(code as CatalogPackageCode))) fail("Plan refers to an unpublished package");
  }
  for (const item of limits) {
    if (!publishedPackages.has(item.package_code)) fail("Limit refers to an unpublished package");
  }
  for (const item of offers) {
    if (!publishedPlans.has(`${item.plan_code}:${item.plan_version}`)) fail("Offer refers to an unpublished plan");
  }

  return {
    ai_complexity_rates: aiComplexityRates,
    ai_token_bundles: aiTokenBundles,
    ai_token_renewal_grant: aiTokenRenewalGrant,
    limits,
    offers,
    packages,
    plans,
    published_at: asDate(source.published_at, "published_at"),
    version: asInteger(source.version, "version", 1)
  };
}

interface CatalogSnapshot {
  catalog: PublicCatalog;
  serialized: string;
  verifiedAt: number;
}

export interface CatalogProjection {
  catalog: PublicCatalog;
  state: "fresh" | "stale";
  ageSeconds: number;
}

export class PublicCatalogCache {
  private snapshot?: CatalogSnapshot;

  constructor(private readonly maxStaleMilliseconds: number) {
    if (!Number.isSafeInteger(maxStaleMilliseconds) || maxStaleMilliseconds < 0) throw new TypeError("maxStaleMilliseconds must be a non-negative integer");
  }

  accept(value: unknown, now: number): PublicCatalog {
    const catalog = normalizePublicCatalog(value);
    const serialized = JSON.stringify(catalog);
    if (this.snapshot && catalog.version < this.snapshot.catalog.version) fail("Catalog version moved backward");
    if (this.snapshot && catalog.version === this.snapshot.catalog.version && serialized !== this.snapshot.serialized) fail("Catalog changed without a version increment");
    this.snapshot = { catalog, serialized, verifiedAt: now };
    return catalog;
  }

  recent(now: number): CatalogProjection | undefined {
    if (!this.snapshot) return undefined;
    const ageMilliseconds = Math.max(0, now - this.snapshot.verifiedAt);
    if (ageMilliseconds > this.maxStaleMilliseconds) return undefined;
    return { catalog: this.snapshot.catalog, state: "stale", ageSeconds: Math.floor(ageMilliseconds / 1000) };
  }
}

export async function resolvePublicCatalog(
  load: () => Promise<unknown>,
  cache: PublicCatalogCache,
  clock: () => number = Date.now
): Promise<CatalogProjection> {
  try {
    return { catalog: cache.accept(await load(), clock()), state: "fresh", ageSeconds: 0 };
  } catch (cause) {
    const recent = cache.recent(clock());
    if (recent) return recent;
    throw new PublicCatalogUnavailableError({ cause });
  }
}
