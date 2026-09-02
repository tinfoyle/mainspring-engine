<script setup lang="ts">
import {
  APIProblem,
  changeMembershipRole,
  createInvitation,
  currentMembership,
  leaveAccount,
  listMemberships,
  reactivateMembership,
  removeMembership,
  suspendMembership,
  transferOwnership,
  type AssignableMembershipRole,
  type Membership
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, reactive, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { useSafeNavigation } from "../composables/useSafeNavigation";
import { useSessionStore } from "../stores/session";

type TeamAction = "role" | "suspend" | "reactivate" | "remove" | "transfer" | "leave";

const session = useSessionStore();
const router = useRouter();
const members = ref<ReadonlyArray<Membership>>([]);
const actor = ref<Membership>();
const loading = ref(false);
const saving = ref(false);
const error = ref("");
const announcement = ref("");
const navigationNotice = ref("");
const securityRequired = ref(false);
const invitationMessage = ref("");
const invitation = reactive<{ email: string; role: AssignableMembershipRole }>({ email: "", role: "member" });
const actionOpen = ref(false);
const action = ref<TeamAction>("role");
const target = ref<Membership>();
const actionRole = ref<AssignableMembershipRole>("member");
const actionReason = ref("");
const confirmation = ref("");
let loadSequence = 0;

const selected = computed(() => session.selected);
const canManageTeam = computed(() => selected.value?.role === "owner" || selected.value?.role === "administrator");
const isOwner = computed(() => selected.value?.role === "owner");
const canLeave = computed(() => actor.value?.state === "active" && actor.value.role !== "owner");
const roles: ReadonlyArray<{ value: AssignableMembershipRole; label: string }> = [
  { value: "administrator", label: "Administrator" }, { value: "billing_admin", label: "Billing admin" },
  { value: "member", label: "Member" }, { value: "viewer", label: "Viewer" }
];
const hasUnsavedAccountWork = computed(() => actionOpen.value || Boolean(invitation.email.trim()));
const { allowNextNavigation } = useSafeNavigation({
  dirty: hasUnsavedAccountWork,
  pending: saving,
  message: "Leave Account administration? Your invitation or team command will be lost.",
  onBlocked: (blockedReason) => {
    navigationNotice.value = blockedReason === "pending"
      ? "This Account change is still being saved. Stay on this page until Spyglass confirms the result."
      : "Navigation canceled. Your invitation or team command remains available.";
  }
});

function label(value: string): string { return value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase()); }
function date(value: string): string { return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)); }
function isSelf(member: Membership): boolean { return member.user_id === session.userID; }
function canManage(member: Membership): boolean {
  return canManageTeam.value && member.role !== "owner" && !isSelf(member) && !(selected.value?.role === "administrator" && member.role === "administrator");
}
function canChangeRole(member: Membership): boolean { return isOwner.value && member.role !== "owner" && !isSelf(member) && member.state === "active"; }
function canTransfer(member: Membership): boolean { return isOwner.value && member.role !== "owner" && !isSelf(member) && member.state === "active"; }
function requiredConfirmation(value: TeamAction): string { return value === "transfer" ? "TRANSFER" : value === "remove" ? "REMOVE" : value === "leave" ? "LEAVE" : ""; }

async function load(): Promise<void> {
  const accountID = session.selectedID;
  const sequence = ++loadSequence;
  actor.value = undefined; members.value = []; securityRequired.value = false; error.value = "";
  if (!accountID) return;
  loading.value = true;
  try {
    actor.value = await currentMembership(accountID);
    if (sequence !== loadSequence) return;
    if (canManageTeam.value) members.value = (await listMemberships(accountID)).memberships;
  } catch (cause) {
    if (sequence === loadSequence) error.value = cause instanceof APIProblem ? cause.message : "Account access is unavailable right now.";
  } finally { if (sequence === loadSequence) loading.value = false; }
}

function handleMutationError(cause: unknown): void {
  if (cause instanceof APIProblem && cause.status === 403 && ["strong_reauthentication_required", "owner_security_enrollment_required"].includes(cause.problem?.code ?? "")) {
    securityRequired.value = true;
    error.value = cause.problem?.code === "owner_security_enrollment_required" ? "Secure this owner Account before changing team access." : "Confirm your identity in Security before changing team access.";
    return;
  }
  error.value = cause instanceof APIProblem ? cause.message : "The Account change could not be completed.";
}

async function invite(): Promise<void> {
  const accountID = session.selectedID;
  if (!accountID || saving.value) return;
  saving.value = true; error.value = ""; securityRequired.value = false; invitationMessage.value = ""; navigationNotice.value = "";
  try {
    const created = await createInvitation(accountID, invitation.email.trim(), invitation.role);
    invitationMessage.value = `Invitation created for ${invitation.email.trim()}. It expires ${date(created.expires_at)}.`;
    announcement.value = invitationMessage.value; invitation.email = ""; invitation.role = "member";
  } catch (cause) { handleMutationError(cause); }
  finally { saving.value = false; }
}

function beginAction(value: TeamAction, member?: Membership): void {
  action.value = value; target.value = member; actionRole.value = member?.role === "owner" ? "administrator" : (member?.role as AssignableMembershipRole ?? "member");
  actionReason.value = ""; confirmation.value = ""; actionOpen.value = true; error.value = ""; securityRequired.value = false;
}

async function submitAction(): Promise<void> {
  const accountID = session.selectedID;
  const current = actor.value;
  if (!accountID || !current || saving.value) return;
  const required = requiredConfirmation(action.value);
  if (required && confirmation.value !== required) return;
  saving.value = true; error.value = ""; securityRequired.value = false; navigationNotice.value = "";
  try {
    const member = target.value;
    if (action.value === "role" && member) await changeMembershipRole(accountID, member, actionRole.value, actionReason.value.trim());
    if (action.value === "suspend" && member) await suspendMembership(accountID, member, actionReason.value.trim());
    if (action.value === "reactivate" && member) await reactivateMembership(accountID, member, actionReason.value.trim());
    if (action.value === "remove" && member) await removeMembership(accountID, member, actionReason.value.trim());
    if (action.value === "transfer" && member) await transferOwnership(accountID, current, member, actionReason.value.trim());
    if (action.value === "leave") await leaveAccount(accountID, current, actionReason.value.trim());
    const completed = action.value;
    actionOpen.value = false; announcement.value = completed === "leave" ? "You left the Account." : `Account team change completed: ${label(completed)}.`;
    if (completed === "transfer" || completed === "leave") await session.load();
    if (completed === "leave") { allowNextNavigation(); await router.push("/app/your-turn"); return; }
    await load();
  } catch (cause) {
    if (cause instanceof APIProblem && cause.status === 409) {
      actionOpen.value = false; await load(); error.value = "This Membership changed. Review the current team before trying again.";
    } else handleMutationError(cause);
  } finally { saving.value = false; }
}

watch(() => session.selectedID, () => void load(), { immediate: true });
</script>

<template>
  <section class="page account-page">
    <p class="sr-only" aria-live="polite" aria-atomic="true">{{ announcement }}</p>
    <p v-if="navigationNotice && !actionOpen" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
    <header class="page-heading"><p class="eyebrow">Account</p><h1>People and authority</h1><p>Invite teammates, keep access current, and make ownership changes with an explicit durable reason.</p></header>

    <section v-if="selected" class="account-summary" aria-labelledby="account-summary-title">
      <div><p class="eyebrow">Selected Account</p><h2 id="account-summary-title">{{ selected.display_name }}</h2></div>
      <dl><div><dt>Your role</dt><dd>{{ label(selected.role) }}</dd></div><div><dt>Account type</dt><dd>{{ label(selected.account_type) }}</dd></div></dl>
    </section>
    <section v-else-if="!loading" class="queue-state"><h2>Select an Account</h2><p>Team access always belongs to one Account.</p></section>

    <section v-if="selected?.owner_enrollment_required" class="queue-state queue-state--warning"><h2>Secure this owner Account first</h2><p>Finish two-factor authentication before using owner authority.</p><a href="/app/security?return_to=%2Fapp%2Faccount">Continue security setup</a></section>
    <p v-if="error" class="queue-inline-status queue-inline-status--error" role="alert">{{ error }} <a v-if="securityRequired" href="/app/security?return_to=%2Fapp%2Faccount">Continue to Security</a></p>
    <section v-if="loading" class="queue-state" role="status"><h2>Loading Account access…</h2></section>

    <template v-else-if="selected && actor">
      <form v-if="canManageTeam" class="invite-form decision-card" @submit.prevent="invite">
        <div><p class="eyebrow">New access</p><h2>Invite a teammate</h2><p class="form-note">Invitations expire. The recipient signs in with their own identity; credentials are never shared.</p></div>
        <label>Email address<input v-model="invitation.email" type="email" autocomplete="email" maxlength="320" required></label>
        <label>Starting role<select v-model="invitation.role"><option v-for="role in roles" :key="role.value" :value="role.value">{{ role.label }}</option></select></label>
        <IoButton type="submit" :disabled="saving">{{ saving ? "Creating…" : "Create invitation" }}</IoButton>
        <p v-if="invitationMessage" class="referral-confirmed" role="status">{{ invitationMessage }}</p>
      </form>

      <section v-if="canManageTeam" class="team-section" aria-labelledby="team-heading">
        <header><div><p class="eyebrow">Current access</p><h2 id="team-heading">Team</h2></div><span>{{ members.length }} Membership{{ members.length === 1 ? "" : "s" }}</span></header>
        <ol class="team-list">
          <li v-for="member in members" :key="member.membership_id" class="team-card">
            <div class="team-identity"><span class="agents-avatar" aria-hidden="true">{{ member.display_name.slice(0, 2).toUpperCase() }}</span><div><strong>{{ member.display_name }} <small v-if="isSelf(member)">(you)</small></strong><a :href="`mailto:${member.email}`">{{ member.email }}</a></div></div>
            <div class="team-meta"><span class="state-badge">{{ label(member.role) }}</span><span :class="['state-badge', member.state === 'suspended' && 'state-badge--warning']">{{ label(member.state) }}</span></div>
            <div v-if="canChangeRole(member) || canTransfer(member) || canManage(member)" class="team-actions">
              <IoButton v-if="canChangeRole(member)" kind="secondary" @click="beginAction('role', member)">Change role</IoButton>
              <IoButton v-if="canManage(member) && member.state === 'active'" kind="secondary" @click="beginAction('suspend', member)">Suspend</IoButton>
              <IoButton v-if="canManage(member) && member.state === 'suspended'" kind="secondary" @click="beginAction('reactivate', member)">Reactivate</IoButton>
              <IoButton v-if="canManage(member)" kind="secondary" @click="beginAction('remove', member)">Remove</IoButton>
              <IoButton v-if="canTransfer(member)" kind="secondary" @click="beginAction('transfer', member)">Transfer ownership</IoButton>
            </div>
          </li>
        </ol>
      </section>
      <section v-else class="queue-state"><h2>Your Account access</h2><p>You are an active {{ label(actor.role) }}. The full team roster is available to owners and administrators.</p></section>

      <section v-if="canLeave" class="account-leave"><div><h2>Leave this Account</h2><p>Leaving is permanent for this Membership. An owner or administrator must invite you again to restore access. Your other Accounts are unaffected.</p></div><IoButton kind="secondary" @click="beginAction('leave')">Leave Account</IoButton></section>
    </template>

    <div v-if="actionOpen" class="modal-backdrop">
      <form class="modal-card decision-card" role="dialog" aria-modal="true" aria-labelledby="team-action-title" @submit.prevent="submitAction">
        <h2 id="team-action-title">{{ action === "leave" ? "Leave this Account?" : `${label(action)} ${target?.display_name ?? "Membership"}?` }}</h2>
        <p class="form-note">This change is bound to the currently loaded Membership version and recorded in the durable audit history.</p>
        <label v-if="action === 'role'">New role<select v-model="actionRole"><option v-for="role in roles" :key="role.value" :value="role.value">{{ role.label }}</option></select></label>
        <label>Operational reason<textarea v-model="actionReason" minlength="3" maxlength="300" rows="4" required></textarea></label>
        <label v-if="requiredConfirmation(action)" class="confirmation-phrase">Type {{ requiredConfirmation(action) }} to confirm<input v-model="confirmation" autocomplete="off" :pattern="requiredConfirmation(action)" required></label>
        <p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
        <p v-if="securityRequired" class="queue-inline-status queue-inline-status--error">{{ error }} <a href="/app/security?return_to=%2Fapp%2Faccount">Continue to Security</a></p>
        <div class="modal-actions"><IoButton type="button" kind="secondary" @click="actionOpen = false">Cancel</IoButton><IoButton type="submit" :disabled="saving || Boolean(requiredConfirmation(action) && confirmation !== requiredConfirmation(action))">{{ saving ? "Saving…" : "Confirm" }}</IoButton></div>
      </form>
    </div>
  </section>
</template>
