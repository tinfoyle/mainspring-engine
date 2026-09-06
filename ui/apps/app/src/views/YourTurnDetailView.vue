<script setup lang="ts">
import {
  APIProblem,
  answerInformation,
  captureOwnerKnowledgeFact,
  confirmActionResolution,
  decideApproval,
  decideWorkReview,
  emitAnalytics,
  getAttentionDetail,
  getMarketingCampaign,
  getMarketingRelease,
  getPrivacyConsent,
  listMatchingFacts,
  requestActionResolution,
  type ActionRecoveryDetail,
  type Approval,
  type AttentionDetail,
  type AttentionKind,
  type InformationRequest,
  type KnowledgeFactSummary,
  type MarketingCampaign,
  type MarketingRelease,
  type WorkReview
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, ref, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { useSafeNavigation } from "../composables/useSafeNavigation";
import { useSessionStore } from "../stores/session";

const route = useRoute();
const router = useRouter();
const session = useSessionStore();
const detail = ref<AttentionDetail>();
const marketingCampaign = ref<MarketingCampaign>();
const marketingRelease = ref<MarketingRelease>();
const facts = ref<ReadonlyArray<KnowledgeFactSummary>>([]);
const loading = ref(false);
const saving = ref(false);
const error = ref("");
const announcement = ref("");
const decision = ref("");
const reason = ref("");
const factID = ref("");
const informationAnswer = ref("");
const confirmed = ref(false);
const navigationNotice = ref("");
let requestSequence = 0;
let openedAt = Date.now();

const kind = computed(() => String(route.params.kind) as AttentionKind);
const itemID = computed(() => String(route.params.id));
const writable = computed(() => detail.value?.kind === "information" || detail.value?.kind === "review"
  ? session.attentionAccess.workWritable : session.attentionAccess.approvalsWritable);
const title = computed(() => {
  const item = detail.value;
  if (!item) return "Decision detail";
  if (item.kind === "information" || item.kind === "review") return item.question;
  return item.capability === "schedules.create" ? "Approve daily report" : item.capability === "marketing.release.activate" ? "Approve marketing campaign" : label(item.capability.replaceAll(".", " "));
});
interface DailyScheduleProposal { name: string; timezone: string; local_hour: number; local_minute: number; subject: string; prompt: string; email_self: boolean; run_now: boolean; source_urls: ReadonlyArray<string> }
const dailySchedule = computed((): DailyScheduleProposal | undefined => {
  const item = detail.value;
  if (item?.kind !== "approval" || item.capability !== "schedules.create") return undefined;
  const value = item.payload as Partial<DailyScheduleProposal>;
  if (typeof value.name !== "string" || typeof value.timezone !== "string" || typeof value.local_hour !== "number"
    || typeof value.local_minute !== "number" || typeof value.subject !== "string" || typeof value.prompt !== "string"
    || typeof value.email_self !== "boolean" || typeof value.run_now !== "boolean"
    || !Array.isArray(value.source_urls) || !value.source_urls.every((url) => typeof url === "string")) return undefined;
  return value as DailyScheduleProposal;
});
const actionLabel = computed(() => {
  if (detail.value?.kind === "information") return "Submit answer";
  if (detail.value?.kind === "review") return "Record review";
  if (detail.value?.kind === "approval") return "Record decision";
  if (detail.value?.state === "manual_resolution") return "Confirm outcome";
  return "Request resolution";
});
const mayConfirmRecovery = computed(() => detail.value?.kind === "action" && detail.value.resolution
  && detail.value.resolution.state === "pending" && detail.value.resolution.requested_by_user_id !== session.userID);
const mayCaptureInformation = computed(() => detail.value?.kind === "information" && detail.value.requirement.scope === "account");
const hasDecisionDraft = computed(() => Boolean(decision.value || reason.value.trim() || factID.value || informationAnswer.value.trim() || confirmed.value));
const marketingApprovalReady = computed(() => {
  const item = detail.value;
  if (item?.kind !== "approval" || item.capability !== "marketing.release.activate") return true;
  const payload = item.payload as { campaign_version?: number; release_version?: number };
  return Boolean(marketingCampaign.value && marketingRelease.value
    && marketingCampaign.value.version === payload.campaign_version
    && marketingRelease.value.version === payload.release_version
    && marketingRelease.value.state === "submitted");
});
const canSubmit = computed(() => {
  const item = detail.value;
  if (!item || !writable.value || saving.value) return false;
  if (item.kind === "approval" && decision.value === "approve" && !marketingApprovalReady.value) return false;
  if (item.kind === "information") return Boolean(factID.value || (mayCaptureInformation.value && informationAnswer.value.trim()));
  if (item.kind === "action" && item.state === "manual_resolution") return Boolean(mayConfirmRecovery.value && confirmed.value);
  return Boolean(decision.value && reason.value.trim().length >= 3 && (item.kind === "review" || confirmed.value));
});

function validKind(value: string): value is AttentionKind {
  return ["information", "review", "approval", "action"].includes(value);
}

function label(value: string): string {
  return value.replaceAll("_", " ").replace(/^./, (letter) => letter.toUpperCase());
}

function formatDate(value?: string): string {
  if (!value) return "Not set";
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? "Unavailable" : new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date);
}

function informationRequester(item: InformationRequest): string {
  return item.requested_by.id.startsWith("agent:") ? "Your Spyglass agent" : "Your team";
}

function draftKey(): string {
  return `spyglass.attention.decision.v1.${session.selectedID}.${kind.value}.${itemID.value}`;
}

function restoreDraft(): void {
  try {
    const value = JSON.parse(sessionStorage.getItem(draftKey()) ?? "null") as { decision?: string; reason?: string; factID?: string; informationAnswer?: string } | null;
    if (!value) return;
    decision.value = value.decision ?? "";
    reason.value = value.reason ?? "";
    factID.value = value.factID ?? "";
    informationAnswer.value = value.informationAnswer ?? "";
  } catch {
    // Tab storage is an enhancement, never a decision authority.
  }
}

function saveDraft(): void {
  try {
    sessionStorage.setItem(draftKey(), JSON.stringify({ decision: decision.value, reason: reason.value, factID: factID.value, informationAnswer: informationAnswer.value }));
  } catch {
    // Tab storage may be unavailable.
  }
}

const { allowNextNavigation } = useSafeNavigation({
  dirty: hasDecisionDraft,
  pending: saving,
  message: "Leave this decision? Your draft will remain only in this browser tab until you return.",
  onBlocked: (reason) => {
    navigationNotice.value = reason === "pending"
      ? "This decision is still being saved. Stay on this page until Spyglass confirms the result."
      : "Navigation canceled. Your decision draft remains on this page and in this browser tab.";
  }
});

async function load(): Promise<void> {
  const accountID = session.selectedID;
  if (!validKind(kind.value) || !itemID.value) {
    detail.value = undefined;
    error.value = "That Your Turn route is not valid.";
    return;
  }
  if (!accountID) return;
  const sequence = ++requestSequence;
  loading.value = true;
  error.value = "";
  marketingCampaign.value = undefined;
  marketingRelease.value = undefined;
  try {
    const result = await getAttentionDetail(accountID, kind.value, itemID.value);
    if (sequence !== requestSequence) return;
    detail.value = result;
    if (result.kind === "approval" && result.capability === "marketing.release.activate") {
      const payload = result.payload as { campaign_id?: string; release_id?: string };
      if (payload.campaign_id && payload.release_id) {
        const [campaign, release] = await Promise.all([
          getMarketingCampaign(accountID, payload.campaign_id),
          getMarketingRelease(accountID, payload.release_id)
        ]);
        if (sequence !== requestSequence) return;
        marketingCampaign.value = campaign;
        marketingRelease.value = release;
      }
    }
    openedAt = Date.now();
    facts.value = result.kind === "information" ? await listMatchingFacts(accountID, result.requirement) : [];
    restoreDraft();
  } catch (cause) {
    if (sequence !== requestSequence) return;
    error.value = cause instanceof APIProblem ? cause.message : "This decision detail is unavailable right now.";
  } finally {
    if (sequence === requestSequence) loading.value = false;
  }
}

async function complete(): Promise<void> {
  const accountID = session.selectedID;
  const item = detail.value;
  if (!accountID || !item || !canSubmit.value) return;
  saving.value = true;
  error.value = "";
  navigationNotice.value = "";
  saveDraft();
  try {
    if (item.kind === "information") {
      let fact = facts.value.find((value) => value.id === factID.value);
      if (!fact && mayCaptureInformation.value && informationAnswer.value.trim()) {
        fact = await captureOwnerKnowledgeFact(accountID, item.requirement.key, informationAnswer.value, `attention/${item.id}/${item.requirement.key}`);
      }
      if (!fact) throw new Error("Answer the question or choose an answer already saved.");
      await answerInformation(accountID, item as InformationRequest, { fact_id: fact.id, fact_version: fact.revision, requirement: item.requirement });
    } else if (item.kind === "review") {
      await decideWorkReview(accountID, item as WorkReview, { decision: decision.value as "approve" | "request_changes", reason: reason.value.trim() });
    } else if (item.kind === "approval") {
      await decideApproval(accountID, item as Approval, { decision: decision.value as "approve" | "reject", reason: reason.value.trim() });
    } else if (item.state === "manual_resolution") {
      await confirmActionResolution(accountID, item as ActionRecoveryDetail);
    } else {
      await requestActionResolution(accountID, item as ActionRecoveryDetail, { outcome: decision.value as "succeeded" | "failed", reason: reason.value.trim() });
    }
    sessionStorage.removeItem(draftKey());
    announcement.value = `${label(item.kind)} completed.`;
    try {
      const consent = await getPrivacyConsent();
      const elapsed = Date.now() - openedAt;
      await emitAnalytics(consent.decided && consent.analytics && !consent.renewal_required, {
        name: "your_turn_item_completed", fields: {
          task_category: item.kind,
          result: "completed",
          duration_bucket: elapsed < 60_000 ? "under_1m" : elapsed < 300_000 ? "1m_5m" : "over_5m"
        }
      });
    } catch {
      // Optional analytics never changes a decision result.
    }
    allowNextNavigation();
    await router.replace({ path: "/app/your-turn", query: { completed: item.kind } });
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : "Spyglass could not save this decision.";
    if (cause instanceof APIProblem && (cause.status === 412 || cause.problem?.code === "attention_version_conflict")) {
      confirmed.value = false;
      announcement.value = "This item changed. The latest version is loading; your draft is preserved.";
      await load();
    }
  } finally {
    saving.value = false;
  }
}

watch(() => [session.selectedID, route.params.kind, route.params.id], () => void load(), { immediate: true });
watch([decision, reason, factID, informationAnswer], saveDraft);
</script>

<template>
  <section class="page attention-detail-page">
    <p class="sr-only" role="status" aria-live="polite">{{ announcement }}</p>
    <RouterLink class="back-link" to="/app/your-turn">← Back to Your Turn</RouterLink>
    <section v-if="loading" class="queue-state" aria-busy="true"><h1>Loading decision…</h1></section>
    <section v-else-if="error && !detail" class="queue-state queue-state--error" role="alert"><h1>That did not load cleanly</h1><p>{{ error }}</p><IoButton kind="secondary" @click="load">Try again</IoButton></section>
    <template v-else-if="detail">
      <header class="detail-heading">
        <div><p class="eyebrow">{{ label(detail.kind) }}</p><h1>{{ title }}</h1></div><span class="state-badge">{{ label(detail.state) }}</span>
      </header>

      <div class="detail-layout">
        <section class="detail-card">
          <h2>Decision context</h2>
          <dl v-if="detail.kind === 'information'">
            <div><dt>Asked by</dt><dd>{{ informationRequester(detail) }}</dd></div><div><dt>Related work</dt><dd><RouterLink :to="`/app/work/${detail.parent_work_item_id}`">Open the Work item</RouterLink></dd></div><div><dt>Requested</dt><dd>{{ formatDate(detail.created_at) }}</dd></div>
          </dl>
          <dl v-else-if="detail.kind === 'review'">
            <div><dt>Work item</dt><dd class="digest">{{ detail.work_item_id }}</dd></div><div><dt>Work version</dt><dd>{{ detail.work_version }}</dd></div><div><dt>Proposal digest</dt><dd class="digest">{{ detail.proposal_sha256 }}</dd></div><div><dt>Requested</dt><dd>{{ formatDate(detail.created_at) }}</dd></div>
          </dl>
          <details v-if="detail.kind === 'information'"><summary>Technical details</summary><dl><div><dt>Knowledge key</dt><dd>{{ detail.requirement.key }}</dd></div><div><dt>Scope</dt><dd>{{ label(detail.requirement.scope) }}</dd></div></dl></details>
          <template v-else-if="detail.kind === 'approval'">
            <template v-if="marketingCampaign && marketingRelease">
              <dl><div><dt>Campaign</dt><dd>{{ marketingCampaign.name }}</dd></div><div><dt>Purpose</dt><dd>{{ marketingCampaign.objective }}</dd></div><div><dt>Audience</dt><dd>{{ marketingCampaign.audience }}</dd></div><div><dt>Release</dt><dd>{{ marketingRelease.name }}</dd></div><div><dt>Channels</dt><dd>{{ marketingRelease.channels.map(label).join(', ') }}</dd></div><div><dt>Content items</dt><dd>{{ marketingRelease.asset_revision_ids.length }}</dd></div></dl>
              <p>Approval marks this release as approved and makes it the campaign’s active release. It does not send or publish content.</p>
              <RouterLink :to="`/app/marketing/releases/${marketingRelease.id}`">Review release content</RouterLink>
            </template>
            <p v-if="!marketingApprovalReady" class="form-error">This campaign or release has changed, or its details could not be loaded. Reject this request and create a new release before approving.</p>
            <p>Decision due: {{ formatDate(detail.expires_at) }}</p>
            <details><summary>Technical details</summary><dl><div><dt>Operation</dt><dd class="digest">{{ detail.operation_id }}</dd></div><div><dt>Policy version</dt><dd>{{ detail.policy_version }}</dd></div><div><dt>Evidence digest</dt><dd class="digest">{{ detail.evidence_sha256 }}</dd></div></dl><pre>{{ JSON.stringify(detail.payload, null, 2) }}</pre></details>
            <section v-if="dailySchedule" class="payload">
              <h2>{{ dailySchedule.name }}</h2>
              <dl>
                <div><dt>When</dt><dd>Every day at {{ String(dailySchedule.local_hour).padStart(2, "0") }}:{{ String(dailySchedule.local_minute).padStart(2, "0") }} ({{ dailySchedule.timezone }})</dd></div>
                <div><dt>Email delivery</dt><dd>{{ dailySchedule.email_self ? "Send each report to your verified account email. Approving gives permission for these daily emails." : "Save reports in Spyglass without emailing them." }}</dd></div>
                <div><dt>First report</dt><dd>{{ dailySchedule.run_now ? "Also run once as soon as you approve." : "Run at the next scheduled time." }}</dd></div>
                <div><dt>Report title</dt><dd>{{ dailySchedule.subject }}</dd></div>
              </dl>
              <p>{{ dailySchedule.prompt }}</p>
              <p v-if="dailySchedule.source_urls.length">Sources: {{ dailySchedule.source_urls.join(", ") }}</p>
              <p>You can pause future reports from Schedules.</p>
            </section>
            <section v-else-if="!marketingRelease" class="payload"><h2>Proposed action</h2><pre>{{ JSON.stringify(detail.payload, null, 2) }}</pre></section>
          </template>
          <dl v-else-if="detail.kind === 'action'">
            <div><dt>Operation</dt><dd class="digest">{{ detail.operation_id }}</dd></div><div><dt>Attempt</dt><dd>{{ detail.attempt_count }}</dd></div><div><dt>Stable error</dt><dd>{{ detail.last_error_code ?? 'None' }}</dd></div><div><dt>Updated</dt><dd>{{ formatDate(detail.updated_at) }}</dd></div>
          </dl>
        </section>

        <form class="decision-card" @submit.prevent="complete">
          <div><h2>{{ actionLabel }}</h2></div>

          <template v-if="detail.kind === 'information'">
            <template v-if="mayCaptureInformation">
              <label for="information-answer">Your answer</label>
              <textarea id="information-answer" v-model="informationAnswer" maxlength="4000" rows="5" placeholder="Answer in your own words" />
              <p class="form-note">Spyglass will save this answer to your business Knowledge and return it to the agent doing the Work.</p>
            </template>
            <template v-if="facts.length">
              <label for="matching-fact">Or use an answer already saved</label>
              <select id="matching-fact" v-model="factID"><option value="">Choose a saved answer</option><option v-for="fact in facts" :key="fact.id" :value="fact.id">{{ fact.key }} · revision {{ fact.revision }}</option></select>
            </template>
            <p v-else-if="!mayCaptureInformation" class="form-note">No current matching answer is available. Add or approve it in Knowledge, then return here.</p>
          </template>

          <template v-else-if="detail.kind === 'review'">
            <fieldset><legend>Review decision</legend><label><input v-model="decision" type="radio" value="approve" /> Approve this Work version</label><label><input v-model="decision" type="radio" value="request_changes" /> Request changes</label></fieldset>
            <label for="review-reason">Reason</label><textarea id="review-reason" v-model="reason" minlength="3" maxlength="1000" rows="5" required />
          </template>

          <template v-else-if="detail.kind === 'approval'">
            <fieldset><legend>Your decision</legend><label><input v-model="decision" type="radio" value="approve" /> Approve this action</label><label><input v-model="decision" type="radio" value="reject" /> Reject action</label></fieldset>
            <label for="approval-reason">Reason</label><textarea id="approval-reason" v-model="reason" minlength="3" maxlength="1000" rows="5" required />
            <label class="confirmation"><input v-model="confirmed" type="checkbox" /> I reviewed the proposed action and understand what will change.</label>
          </template>

          <template v-else-if="detail.state === 'unknown'">
            <fieldset><legend>Observed provider outcome</legend><label><input v-model="decision" type="radio" value="succeeded" /> Succeeded</label><label><input v-model="decision" type="radio" value="failed" /> Failed</label></fieldset>
            <label for="recovery-reason">Evidence-based reason</label><textarea id="recovery-reason" v-model="reason" minlength="3" maxlength="1000" rows="5" required />
            <label class="confirmation"><input v-model="confirmed" type="checkbox" /> I understand a different eligible operator must confirm this outcome.</label>
          </template>

          <template v-else>
            <p v-if="detail.resolution">Requested outcome: <strong>{{ label(detail.resolution.requested_outcome) }}</strong></p>
            <p v-if="!mayConfirmRecovery" class="form-note">A different eligible Owner or Administrator must confirm this outcome.</p>
            <label v-else class="confirmation"><input v-model="confirmed" type="checkbox" /> I independently verified this observed outcome.</label>
          </template>


          <p v-if="!writable" class="form-note">This Account or package is read-only for this decision.</p>
          <p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
          <p v-if="error" class="form-error" role="alert">{{ error }}</p>
          <IoButton type="submit" :disabled="!canSubmit">{{ saving ? "Saving…" : actionLabel }}</IoButton>
        </form>
      </div>
    </template>
  </section>
</template>
