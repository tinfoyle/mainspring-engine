import { afterEach, describe, expect, it, vi } from "vitest";
import type { Membership } from "./generated/api-types";
import { changeMembershipRole, currentMembership, leaveAccount, transferOwnership } from "./account-team";

afterEach(() => vi.unstubAllGlobals());

const owner = { membership_id: "10000000-0000-4000-8000-000000000001", user_id: "20000000-0000-4000-8000-000000000002", display_name: "Owner", email: "owner@example.com", role: "owner", state: "active", version: 5, created_at: "2026-08-24T20:00:00Z" } satisfies Membership;
const target = { ...owner, membership_id: "30000000-0000-4000-8000-000000000003", user_id: "40000000-0000-4000-8000-000000000004", display_name: "Operator", email: "operator@example.com", role: "administrator", version: 3 } satisfies Membership;

describe("Account team client", () => {

  it("reads the caller's current Membership without roster authority", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ membership: target }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    await expect(currentMembership("account")).resolves.toEqual(target);
    expect(String(fetcher.mock.calls[0]?.[0])).toBe("/api/v1/accounts/account/membership");
  });

  it("binds role changes and leaving to the current Membership version", async () => {
    const fetcher = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ membership: target }), { status: 200, headers: { "content-type": "application/json" } }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetcher);
    await changeMembershipRole("account", target, "viewer", "Reduce access.");
    await leaveAccount("account", target, "Leaving this Account.");
    expect(JSON.parse(String(fetcher.mock.calls[0]?.[1]?.body))).toEqual({ expected_version: 3, role: "viewer", reason: "Reduce access." });
    expect(String(fetcher.mock.calls[1]?.[0])).toBe("/api/v1/accounts/account/membership");
    expect(JSON.parse(String(fetcher.mock.calls[1]?.[1]?.body))).toEqual({ expected_version: 3, reason: "Leaving this Account." });
  });

  it("freezes both Membership versions for ownership transfer", async () => {
    const fetcher = vi.fn().mockResolvedValue(new Response(JSON.stringify({ previous_owner: owner, new_owner: target }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetcher);
    await transferOwnership("account", owner, target, "Transfer operating authority.");
    expect(JSON.parse(String(fetcher.mock.calls[0]?.[1]?.body))).toEqual({ target_membership_id: target.membership_id, expected_actor_version: 5, expected_target_version: 3, reason: "Transfer operating authority." });
  });
});
