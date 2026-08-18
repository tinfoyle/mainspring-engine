import type {
  CatalogFeaturePackage,
  CatalogLimitDefinition,
  CatalogOffer,
  CatalogPackageCode,
  CatalogPlan,
  PublicCatalog,
} from "./generated/api-types";

export type DisplayPlan = {
  name: string;
  offerCode?: string;
  price: string;
  cadence: string;
  description: string;
  featured: boolean;
  features: readonly string[];
};

const PACKAGE_CODES = new Set<CatalogPackageCode>(["knowledge", "work", "agents", "finance", "marketing", "integrations"]);
const MACHINE_CODE = /^[a-z][a-z0-9_-]*$/;
const FEATURE_CODE = /^[a-z][a-z0-9_.-]+$/;
const RFC3339_DATE_TIME = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/;

function isObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function hasOnlyKeys(value: Record<string, unknown>, allowed: readonly string[]): boolean {
  const keys = new Set(allowed);
  return Object.keys(value).every((key) => keys.has(key));
}

function isInteger(value: unknown, minimum = 0, maximum = Number.MAX_SAFE_INTEGER): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= minimum && value <= maximum;
}

function isDateTime(value: unknown): value is string {
  return typeof value === "string" && RFC3339_DATE_TIME.test(value) && Number.isFinite(Date.parse(value));
}

function isPackageCode(value: unknown): value is CatalogPackageCode {
  return typeof value === "string" && PACKAGE_CODES.has(value as CatalogPackageCode);
}

function isStringArray(value: unknown, predicate: (item: string) => boolean): value is string[] {
  return Array.isArray(value) && value.every((item) => typeof item === "string" && predicate(item));
}

function isPackage(value: unknown): value is CatalogFeaturePackage {
  if (!isObject(value) || !hasOnlyKeys(value, ["code", "version", "name", "description", "dependencies", "features", "default_limits"])) return false;
  if (!isPackageCode(value.code) || !isInteger(value.version, 1) || typeof value.name !== "string" || value.name.length < 1 || value.name.length > 120 || typeof value.description !== "string" || value.description.length > 1000) return false;
  if (!isStringArray(value.features, (item) => item.length <= 100 && FEATURE_CODE.test(item))) return false;
  if (value.dependencies !== undefined && (!Array.isArray(value.dependencies) || !value.dependencies.every(isPackageCode) || new Set(value.dependencies).size !== value.dependencies.length)) return false;
  if (value.default_limits !== undefined && (!isObject(value.default_limits) || !Object.values(value.default_limits).every((limit) => isInteger(limit)))) return false;
  return true;
}

function isLimit(value: unknown): value is CatalogLimitDefinition {
  if (!isObject(value) || !hasOnlyKeys(value, ["code", "package_code", "name", "unit", "kind", "combine", "reservation_ttl_seconds"])) return false;
  if (typeof value.code !== "string" || value.code.length > 50 || !MACHINE_CODE.test(value.code) || !isPackageCode(value.package_code)) return false;
  if (typeof value.name !== "string" || value.name.length < 1 || value.name.length > 120 || typeof value.unit !== "string" || value.unit.length > 50 || !MACHINE_CODE.test(value.unit)) return false;
  if (value.kind !== "capacity" || !["replace", "add", "maximum", "minimum"].includes(String(value.combine))) return false;
  return value.reservation_ttl_seconds === undefined || isInteger(value.reservation_ttl_seconds, 1, 2_592_000);
}

function isCatalogPlan(value: unknown): value is CatalogPlan {
  if (!isObject(value) || !hasOnlyKeys(value, ["code", "version", "name", "description", "packages"])) return false;
  if (typeof value.code !== "string" || value.code.length > 50 || !MACHINE_CODE.test(value.code) || !isInteger(value.version, 1) || typeof value.name !== "string" || value.name.length < 1 || value.name.length > 120 || typeof value.description !== "string" || value.description.length > 1000 || !isObject(value.packages)) return false;
  return Object.entries(value.packages).every(([code, mode]) => isPackageCode(code) && (mode === "enabled" || mode === "read_only" || mode === "suspended"));
}

function isCatalogOffer(value: unknown): value is CatalogOffer {
  return isObject(value)
    && hasOnlyKeys(value, ["code", "plan_code", "plan_version", "currency", "amount_minor", "billing_interval", "effective_from"])
    && typeof value.code === "string" && value.code.length <= 80 && MACHINE_CODE.test(value.code)
    && typeof value.plan_code === "string" && value.plan_code.length <= 50 && MACHINE_CODE.test(value.plan_code)
    && isInteger(value.plan_version, 1)
    && typeof value.currency === "string" && /^[A-Z]{3}$/.test(value.currency)
    && isInteger(value.amount_minor)
    && (value.billing_interval === "none" || value.billing_interval === "month" || value.billing_interval === "year")
    && isDateTime(value.effective_from);
}

export function isPublicCatalog(value: unknown): value is PublicCatalog {
  return isObject(value)
    && hasOnlyKeys(value, ["version", "published_at", "packages", "limits", "plans", "offers"])
    && isInteger(value.version, 1)
    && isDateTime(value.published_at)
    && Array.isArray(value.packages) && value.packages.length > 0 && value.packages.every(isPackage)
    && Array.isArray(value.limits) && value.limits.every(isLimit)
    && Array.isArray(value.plans) && value.plans.length > 0 && value.plans.every(isCatalogPlan)
    && Array.isArray(value.offers) && value.offers.every(isCatalogOffer);
}

export function publishedPlans(value: unknown, now = Date.now()): DisplayPlan[] | undefined {
  if (!isPublicCatalog(value)) return undefined;
  const planByCode = new Map(value.plans.map((plan) => [plan.code, plan]));
  const published = value.offers.flatMap((offer): DisplayPlan[] => {
    const effective = Date.parse(offer.effective_from);
    if (effective > now) return [];
    const plan = planByCode.get(offer.plan_code);
    if (!plan || plan.version !== offer.plan_version) return [];
    const price = new Intl.NumberFormat("en-US", { style: "currency", currency: offer.currency, maximumFractionDigits: offer.amount_minor % 100 === 0 ? 0 : 2 }).format(offer.amount_minor / 100);
    const packageNames = Object.keys(plan.packages).map((code) => `${code.charAt(0).toUpperCase()}${code.slice(1)} package`);
    return [{ name: plan.name, offerCode: offer.amount_minor > 0 ? offer.code : undefined, price, cadence: offer.amount_minor > 0 ? `per ${offer.billing_interval}` : "forever", description: plan.description, featured: offer.plan_code === "team", features: packageNames }];
  });
  return published.length > 0 ? published : undefined;
}
