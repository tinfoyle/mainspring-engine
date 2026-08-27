import { requestJSON } from "./client";
import type { AITokenBalance, AITokenPromotionRedemption, BillingStatus, CreateCheckoutSessionRequest, CreatePurchaseCheckoutSessionRequest, HostedBillingSession, PublicCatalog, RedeemAITokenPromotionRequest } from "./generated/api-types";

export const getPublicCatalog = (): Promise<PublicCatalog> => requestJSON("/api/v1/catalog/public");

export const getBillingStatus = (accountID: string): Promise<BillingStatus> =>
  requestJSON(`/api/v1/accounts/${encodeURIComponent(accountID)}/billing`);

export const getAITokenBalance = (accountID: string): Promise<AITokenBalance> =>
  requestJSON(`/api/v1/accounts/${encodeURIComponent(accountID)}/ai-tokens`);

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

export const createPurchaseCheckoutSession = (
  accountID: string,
  input: CreatePurchaseCheckoutSessionRequest,
  requestID: string
): Promise<HostedBillingSession> => requestJSON(`/api/v1/accounts/${encodeURIComponent(accountID)}/purchase-checkout-sessions`, {
  method: "POST",
  headers: { "Idempotency-Key": requestID },
  body: JSON.stringify(input)
});

export const redeemAITokenPromotion = (
  accountID: string,
  input: RedeemAITokenPromotionRequest,
  requestID: string
): Promise<AITokenPromotionRedemption> => requestJSON(`/api/v1/accounts/${encodeURIComponent(accountID)}/ai-token-promotions`, {
  method: "POST",
  headers: { "Idempotency-Key": requestID },
  body: JSON.stringify(input)
});
