<script setup lang="ts">
import {
  APIProblem,
  answerInformation,
  confirmActionResolution,
  decideApproval,
  decideWorkReview,
  emitAnalytics,
  getAttentionDetail,
  getPrivacyConsent,
  listMatchingFacts,
  requestActionResolution,
  type ActionRecoveryDetail,
  type Approval,
  type AttentionDetail,
  type AttentionKind,
  type InformationRequest,
  type KnowledgeFactSummary,
  type WorkReview
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, ref, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { useSessionStore } from "../stores/session";

const route = useRoute();
const router = useRouter();
const session = useSessionStore();
const detail = ref<AttentionDetail>();
const facts = ref<ReadonlyArray<KnowledgeFactSummary>>([]);
const loading = ref(false);
const saving = ref(false);
const error = ref("");
const announcement = ref("");
const decision = ref("");
const reason = ref("");
const factID = ref("");
const confirmed = ref(false);
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
  return item.capability;
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
const canSubmit = computed(() => {
  const item = detail.value;
  if (!item || !writable.value || saving.value) return false;
  if (item.kind === "information") return Boolean(factID.value);
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

function draftKey(): string {
  return `spyglass.attention.decision.v1.${session.selectedID}.${kind.value}.${itemID.value}`;
}

function restoreDraft(): void {
  try {
    const value = JSON.parse(sessionStorage.getItem(draftKey()) ?? "null") as { decision?: string; reason?: string; factID?: string } | null;
    if (!value) return;
    decision.value = value.decision ?? "";
    reason.value = value.reason ?? "";
    factID.value = value.factID ?? "";
  } catch {
    // Tab storage is an enhancement, never a decision authority.
  }
}

function saveDraft(): void {
  try {
    sessionStorage.setItem(draftKey(), JSON.stringify({ decision: decision.value, reason: reason.value, factID: factID.value }));
  } catch {
    // Tab storage may be unavailable.
  }
}

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
  try {
    const result = await getAttentionDetail(accountID, kind.value, itemID.value);
    if (sequence !== requestSequence) return;
    detail.value = result;
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
  saveDraft();
  try {
    if (item.kind === "information") {
      const fact = facts.value.find((value) => value.id === factID.value);
      if (!fact) throw new Error("Select a current matching fact.");
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
    await router.replace({ path: "/app/your-turn", query: { completed: item.kind } });
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : "Spyglass could not save this decision.";
    if (cause instanceof APIProblem && (cause.status === 412 || cause.problem?.code === "attention_version_conflict")) {
      announcement.value = "This item changed. The latest version is loading; your draft is preserved.";
      await load();
    }
  } finally {
    saving.value = false;
  }
}

watch(() => [session.selectedID, route.params.kind, route.params.id], () => void load(), { immediate: true });
watch([decision, reason, factID], saveDraft);
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
            <div><dt>Required fact</dt><dd>{{ detail.requirement.key }}</dd></div><div><dt>Scope</dt><dd>{{ label(detail.requirement.scope) }}</dd></div><div><dt>Parent Work</dt><dd class="digest">{{ detail.parent_work_item_id }}</dd></div><div><dt>Requested</dt><dd>{{ formatDate(detail.created_at) }}</dd></div>
          </dl>
          <dl v-else-if="detail.kind === 'review'">
            <div><dt>Work item</dt><dd class="digest">{{ detail.work_item_id }}</dd></div><div><dt>Work version</dt><dd>{{ detail.work_version }}</dd></div><div><dt>Proposal digest</dt><dd class="digest">{{ detail.proposal_sha256 }}</dd></div><div><dt>Requested</dt><dd>{{ formatDate(detail.created_at) }}</dd></div>
          </dl>
          <dl v-else-if="detail.kind === 'approval'">
            <div><dt>Operation</dt><dd class="digest">{{ detail.operation_id }}</dd></div><div><dt>Policy version</dt><dd>{{ detail.policy_version }}</dd></div><div><dt>Evidence digest</dt><dd class="digest">{{ detail.evidence_sha256 }}</dd></div><div><dt>Expires</dt><dd>{{ formatDate(detail.expires_at) }}</dd></div>
          </dl>
          <dl v-else>
            <div><dt>Operation</dt><dd class="digest">{{ detail.operation_id }}</dd></div><div><dt>Attempt</dt><dd>{{ detail.attempt_count }}</dd></div><div><dt>Stable error</dt><dd>{{ detail.last_error_code ?? 'None' }}</dd></div><div><dt>Updated</dt><dd>{{ formatDate(detail.updated_at) }}</dd></div>
          </dl>
          <section v-if="detail.kind === 'approval'" class="payload"><h2>Exact proposed payload</h2><pre>{{ JSON.stringify(detail.payload, null, 2) }}</pre></section>
        </section>

        <form class="decision-card" @submit.prevent="complete">
          <div><p class="eyebrow">Your judgment</p><h2>{{ actionLabel }}</h2></div>

          <template v-if="detail.kind === 'information'">
            <label for="matching-fact">Current matching fact</label>
            <select id="matching-fact" v-model="factID" required><option value="">Select a fact</option><option v-for="fact in facts" :key="fact.id" :value="fact.id">{{ fact.key }} · revision {{ fact.revision }} · {{ fact.sensitivity }}</option></select>
            <p v-if="facts.length === 0" class="form-note">No active exact-match fact is available. Add or approve it in Knowledge, then return here.</p>
          </template>

          <template v-else-if="detail.kind === 'review'">
            <fieldset><legend>Review decision</legend><label><input v-model="decision" type="radio" value="approve" /> Approve this Work version</label><label><input v-model="decision" type="radio" value="request_changes" /> Request changes</label></fieldset>
            <label for="review-reason">Decision reason</label><textarea id="review-reason" v-model="reason" minlength="3" maxlength="1000" rows="5" required />
          </template>

          <template v-else-if="detail.kind === 'approval'">
            <fieldset><legend>Consequential action</legend><label><input v-model="decision" type="radio" value="approve" /> Approve exact action</label><label><input v-model="decision" type="radio" value="reject" /> Reject action</label></fieldset>
            <label for="approval-reason">Decision reason</label><textarea id="approval-reason" v-model="reason" minlength="3" maxlength="1000" rows="5" required />
            <label class="confirmation"><input v-model="confirmed" type="checkbox" /> I reviewed the exact payload, evidence digest and frozen policy above.</label>
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

          <p class="form-note">Your draft stays in this browser tab until Spyglass accepts it.</p>
          <p v-if="!writable" class="form-note">This Account or package is read-only for this decision.</p>
          <p v-if="error" class="form-error" role="alert">{{ error }}</p>
          <IoButton type="submit" :disabled="!canSubmit">{{ saving ? "Saving…" : actionLabel }}</IoButton>
        </form>
      </div>
    </template>
  </section>
</template>
