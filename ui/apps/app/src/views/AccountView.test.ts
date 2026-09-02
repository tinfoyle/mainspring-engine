// @vitest-environment happy-dom
import { flushPromises, mount } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createMemoryHistory, createRouter } from "vue-router";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { APIProblem, type AccountChoice, type Membership } from "@spyglass/api";
import { useSessionStore } from "../stores/session";
import { expectNoAxeViolations } from "../test/accessibility";
import AccountView from "./AccountView.vue";

const api = vi.hoisted(() => ({
  currentMembership: vi.fn(), listMemberships: vi.fn(), createInvitation: vi.fn(), changeMembershipRole: vi.fn(),
  suspendMembership: vi.fn(), reactivateMembership: vi.fn(), removeMembership: vi.fn(), leaveAccount: vi.fn(), transferOwnership: vi.fn()
}));
vi.mock("@spyglass/api", async (importOriginal) => ({ ...await importOriginal<typeof import("@spyglass/api")>(), ...api }));

const userID = "50000000-0000-4000-8000-000000000005";
const account = {
  account_id: "10000000-0000-4000-8000-000000000001", account_type: "paid", account_state: "active", account_version: 1,
  cell_id: "cell-us-east-01", display_name: "Northstar Studio", placement_generation: 1, role: "owner",
  slug: "northstar-studio", owner_enrollment_required: false,
  entitlements: { account_id: "10000000-0000-4000-8000-000000000001", catalog_version: 2, evaluated_at: "2026-08-24T20:00:00Z", version: 3, packages: [] }
} satisfies AccountChoice;
const owner = { membership_id: "20000000-0000-4000-8000-000000000002", user_id: userID, display_name: "Casey Owner", email: "casey@example.com", role: "owner", state: "active", version: 5, created_at: "2026-08-20T20:00:00Z" } satisfies Membership;
const member = { membership_id: "30000000-0000-4000-8000-000000000003", user_id: "40000000-0000-4000-8000-000000000004", display_name: "Morgan Member", email: "morgan@example.com", role: "member", state: "active", version: 2, created_at: "2026-08-21T20:00:00Z" } satisfies Membership;

async function render() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: "/app/account", component: AccountView }, { path: "/app/your-turn", component: { template: "<h1>Your Turn</h1>" } }] });
  await router.push("/app/account"); await router.isReady();
  const wrapper = mount(AccountView, { global: { plugins: [router] } });
  await flushPromises();
  await expectNoAxeViolations(wrapper.element);
  return wrapper;
}

beforeEach(() => {
  setActivePinia(createPinia());
  for (const mock of Object.values(api)) mock.mockReset();
  api.currentMembership.mockResolvedValue(owner); api.listMemberships.mockResolvedValue({ memberships: [owner, member] });
  const session = useSessionStore(); session.accounts = [account]; session.selectedID = account.account_id; session.userID = userID;
});

describe("Account team surface", () => {
  it("shows owner-authorized invitation and Membership controls", async () => {
    const wrapper = await render();
    expect(api.currentMembership).toHaveBeenCalledWith(account.account_id);
    expect(api.listMemberships).toHaveBeenCalledWith(account.account_id);
    expect(wrapper.text()).toContain("Invite a teammate");
    expect(wrapper.text()).toContain("Morgan Member");
    expect(wrapper.findAll("button").some((button) => button.text() === "Transfer ownership")).toBe(true);
  });

  it("reloads the current roster after a Membership conflict", async () => {
    api.changeMembershipRole.mockRejectedValue(new APIProblem(409));
    const wrapper = await render();
    await wrapper.findAll("button").find((button) => button.text() === "Change role")?.trigger("click");
    await wrapper.get("[role=dialog] textarea").setValue("Responsibilities changed.");
    await wrapper.get("form[role=dialog]").trigger("submit");
    await flushPromises();
    expect(api.changeMembershipRole).toHaveBeenCalledWith(account.account_id, member, "member", "Responsibilities changed.");
    expect(api.listMemberships).toHaveBeenCalledTimes(2);
    expect(wrapper.text()).toContain("This Membership changed. Review the current team before trying again.");
  });

  it("offers the Security continuation when strong authentication is required", async () => {
    api.createInvitation.mockRejectedValue(new APIProblem(403, { code: "strong_reauthentication_required", detail: "confirm with a passkey", status: 403, title: "Forbidden", type: "about:blank" }));
    const wrapper = await render();
    await wrapper.get(".invite-form input[type=email]").setValue("new@example.com");
    await wrapper.get(".invite-form").trigger("submit"); await flushPromises();
    expect(wrapper.text()).toContain("Confirm your identity in Security before changing team access.");
    expect(wrapper.get('a[href="/app/security?return_to=%2Fapp%2Faccount"]').text()).toBe("Continue to Security");
  });
});
