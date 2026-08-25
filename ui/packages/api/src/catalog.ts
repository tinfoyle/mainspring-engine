import { requestJSON } from "./client";
import type { BillingStatus, CreateCheckoutSessionRequest, HostedBillingSession, PublicCatalog } from "./generated/api-types";

export const getPublicCatalog = (): Promise<PublicCatalog> => requestJSON("/api/v1/catalog/public");

export const getBillingStatus = (accountID: string): Promise<BillingStatus> =>
  requestJSON(`/api/v1/accounts/${encodeURIComponent(accountID)}/billing`);

export const createCheckoutSession = (
  accountID: string,
  input: CreateCheckoutSessionRequest,
  requestID: string
): Promise<HostedBillingSession> => requestJSON(`/api/v1/accounts/${encodeURIComponent(accountID)}/checkout-sessions`, {
  method: "POST",
  headers: { "Idempotency-Key": requestID },
  body: JSON.stringify(input)
});

export const createBillingPortalSession = (accountID: string, requestID: string): Promise<HostedBillingSession> =>
  requestJSON(`/api/v1/accounts/${encodeURIComponent(accountID)}/billing-portal-sessions`, {
    method: "POST",
    headers: { "Idempotency-Key": requestID }
  });
