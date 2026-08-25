<script setup lang="ts">
import type { AgentPersona, AgentToolGrantInput, PublishAgentPersonaRequest } from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, ref, watch } from "vue";

type ToolCapability = AgentToolGrantInput["capability"];
interface ToolDefinition { capability: ToolCapability; name: string; label: string; description: string; input_schema: Readonly<Record<string, unknown>> }
const uuid = { type: "string", format: "uuid" } as const;
const closed = (properties: Readonly<Record<string, unknown>>, required: ReadonlyArray<string> = []): Readonly<Record<string, unknown>> => ({ type: "object", additionalProperties: false, properties, ...(required.length ? { required } : {}) });
const tools: ReadonlyArray<ToolDefinition> = [
  { capability: "work.summary.read", name: "read_work_summary", label: "Read Work summary", description: "Read the current Account Work summary.", input_schema: closed({}) },
  { capability: "finance.ledgers.read", name: "read_finance_ledgers", label: "Read Finance ledgers", description: "List the Account's governed ledgers.", input_schema: closed({}) },
  { capability: "finance.accounts.read", name: "read_finance_accounts", label: "Read chart of accounts", description: "List posting accounts for one governed ledger.", input_schema: closed({ ledger_id: uuid }, ["ledger_id"]) },
  { capability: "finance.entry.draft", name: "draft_finance_entry", label: "Draft Finance entry", description: "Prepare a balanced draft journal entry; posting remains a separate consequential action.", input_schema: closed({ ledger_id: uuid, run_id: uuid, entry_date: { type: "string", format: "date-time" }, description: { type: "string" }, reference: { type: "string" }, currency: { type: "string", pattern: "^[A-Z]{3}$" }, lines: { type: "array", minItems: 2, items: closed({ account_id: uuid, memo: { type: "string" }, debit_minor: { type: "integer", minimum: 0 }, credit_minor: { type: "integer", minimum: 0 } }, ["account_id", "memo", "debit_minor", "credit_minor"]) }, evidence: { type: "array", items: uuid } }, ["ledger_id", "run_id", "entry_date", "description", "reference", "currency", "lines", "evidence"]) },
  { capability: "marketing.campaigns.read", name: "read_marketing_campaigns", label: "Read Marketing campaigns", description: "List campaign intent, optionally filtered by state.", input_schema: closed({ state: { type: "string", enum: ["draft", "active", "paused", "completed", "archived"] } }) },
  { capability: "marketing.asset-revisions.read", name: "read_marketing_creative", label: "Read creative revisions", description: "List immutable creative revisions for a campaign.", input_schema: closed({ campaign_id: uuid, asset_id: uuid }, ["campaign_id"]) },
  { capability: "marketing.releases.read", name: "read_marketing_releases", label: "Read Marketing releases", description: "List frozen release snapshots for a campaign.", input_schema: closed({ campaign_id: uuid }, ["campaign_id"]) },
  { capability: "marketing.campaign.draft", name: "draft_marketing_campaign", label: "Draft campaign", description: "Prepare additive campaign intent without activating or delivering it.", input_schema: closed({ run_id: uuid, name: { type: "string" }, objective: { type: "string" }, audience: { type: "string" }, channels: { type: "array", minItems: 1, uniqueItems: true, items: { type: "string", enum: ["email", "web"] } } }, ["run_id", "name", "objective", "audience", "channels"]) },
  { capability: "marketing.asset-revision.draft", name: "draft_marketing_copy", label: "Draft Marketing copy", description: "Append bounded UTF-8 copy through governed immutable creative admission.", input_schema: closed({ run_id: uuid, campaign_id: uuid, asset_id: uuid, title: { type: "string" }, content: { type: "string", maxLength: 65536 } }, ["run_id", "campaign_id", "asset_id", "title", "content"]) },
  { capability: "marketing.release.draft", name: "draft_marketing_release", label: "Draft release snapshot", description: "Prepare an exact release snapshot without submitting, approving or activating it.", input_schema: closed({ run_id: uuid, campaign_id: uuid, campaign_version: { type: "integer", minimum: 1 }, name: { type: "string" }, channels: { type: "array", minItems: 1, uniqueItems: true, items: { type: "string", enum: ["email", "web"] } }, asset_revision_ids: { type: "array", minItems: 1, maxItems: 100, uniqueItems: true, items: uuid } }, ["run_id", "campaign_id", "campaign_version", "name", "channels", "asset_revision_ids"]) }
];
const actions = [
  { capability: "stripe.customer.create", label: "Propose Stripe customer creation" },
  { capability: "finance.entry.post", label: "Propose Finance entry posting" },
  { capability: "marketing.release.activate", label: "Propose Marketing release activation" }
] as const;

const props = defineProps<{ persona?: AgentPersona | undefined; saving: boolean; error?: string | undefined }>();
const emit = defineEmits<{ close: []; publish: [input: PublishAgentPersonaRequest]; "dirty-change": [dirty: boolean] }>();
const name = ref(props.persona?.name ?? ""); const role = ref(props.persona?.role ?? ""); const description = ref(props.persona?.description ?? ""); const instructions = ref(props.persona?.system_instructions ?? "");
const provider = ref(props.persona?.policy.provider ?? "openai"); const model = ref(props.persona?.policy.model ?? "gpt-5.4"); const fallbackModels = ref(props.persona?.policy.fallback_models.join(", ") ?? ""); const reasoningEffort = ref(props.persona?.policy.reasoning_effort ?? "medium");
const maximumInputTokens = ref(props.persona?.policy.maximum_input_tokens ?? 128000); const maximumOutputTokens = ref(props.persona?.policy.maximum_output_tokens ?? 4096); const maximumCostDollars = ref((props.persona?.policy.maximum_cost_micros ?? 500000) / 1_000_000); const maximumToolSteps = ref(props.persona?.policy.maximum_tool_steps ?? 2);
const citationPolicy = ref<"none" | "required" | "best_effort">(props.persona?.policy.citation_policy ?? "best_effort"); const actionPolicy = ref<"none" | "propose">(props.persona?.policy.action_policy ?? "none");
const toolCapabilities = ref<ToolCapability[]>(props.persona?.policy.tools.map((item) => item.capability) ?? []); const actionCapabilities = ref<string[]>([...(props.persona?.policy.action_capabilities ?? [])]);
const advancedOpen = ref(Boolean(props.persona));
const title = computed(() => props.persona ? `Publish ${props.persona.name} version ${props.persona.latest_version + 1}` : "Publish a Persona");
function snapshot(): string {
  return JSON.stringify({
    name: name.value,
    role: role.value,
    description: description.value,
    instructions: instructions.value,
    provider: provider.value,
    model: model.value,
    fallbackModels: fallbackModels.value,
    reasoningEffort: reasoningEffort.value,
    maximumInputTokens: maximumInputTokens.value,
    maximumOutputTokens: maximumOutputTokens.value,
    maximumCostDollars: maximumCostDollars.value,
    maximumToolSteps: maximumToolSteps.value,
    citationPolicy: citationPolicy.value,
    actionPolicy: actionPolicy.value,
    toolCapabilities: [...toolCapabilities.value].sort(),
    actionCapabilities: [...actionCapabilities.value].sort()
  });
}
const initialSnapshot = snapshot();
const dirty = computed(() => snapshot() !== initialSnapshot);
watch(dirty, (value) => emit("dirty-change", value), { immediate: true });

function toggleTool(capability: ToolCapability): void { toolCapabilities.value = toolCapabilities.value.includes(capability) ? toolCapabilities.value.filter((item) => item !== capability) : [...toolCapabilities.value, capability]; }
function toggleAction(capability: string): void { actionCapabilities.value = actionCapabilities.value.includes(capability) ? actionCapabilities.value.filter((item) => item !== capability) : [...actionCapabilities.value, capability]; }
function toggleAdvanced(event: Event): void { advancedOpen.value = (event.target as HTMLDetailsElement).open; }
function publish(): void {
  const selectedTools: AgentToolGrantInput[] = toolCapabilities.value.map((capability) => { const value = tools.find((item) => item.capability === capability)!; return { name: value.name, capability: value.capability, description: value.description, input_schema: value.input_schema }; });
  const fallbacks = fallbackModels.value.split(",").map((item) => item.trim()).filter(Boolean).slice(0, 2);
  emit("publish", {
    persona_id: props.persona?.id ?? crypto.randomUUID(), expected_latest_version: props.persona?.latest_version ?? 0,
    name: name.value.trim(), role: role.value.trim(), description: description.value.trim(), system_instructions: instructions.value.trim(),
    policy: { provider: provider.value.trim(), model: model.value.trim(), fallback_models: fallbacks, ...(reasoningEffort.value.trim() ? { reasoning_effort: reasoningEffort.value.trim() } : {}), maximum_input_tokens: maximumInputTokens.value, maximum_output_tokens: maximumOutputTokens.value, maximum_cost_micros: Math.round(maximumCostDollars.value * 1_000_000), maximum_tool_steps: maximumToolSteps.value, citation_policy: citationPolicy.value, action_policy: actionPolicy.value, ...(actionPolicy.value === "propose" && actionCapabilities.value.length ? { action_capabilities: actionCapabilities.value } : {}), tools: selectedTools }
  });
}
</script>

<template>
  <div class="modal-backdrop">
    <form class="modal-card persona-modal" role="dialog" aria-modal="true" aria-labelledby="persona-editor-title" @submit.prevent="publish">
      <div class="persona-modal-heading"><div><p class="eyebrow">Immutable specialist policy</p><h2 id="persona-editor-title">{{ title }}</h2></div><button type="button" aria-label="Close Persona editor" @click="emit('close')">×</button></div>
      <label>Name<input v-model="name" minlength="2" maxlength="120" required></label><label>Role<input v-model="role" minlength="2" maxlength="160" required></label><label>Description<textarea v-model="description" maxlength="4000" rows="3"></textarea></label><label>System instructions<textarea v-model="instructions" minlength="20" maxlength="32768" rows="7" required></textarea><small>Write the durable operating boundaries this specialist must follow. Published versions cannot be edited.</small></label>
      <fieldset><legend>Evidence and action policy</legend><label>Citations<select v-model="citationPolicy"><option value="none">No citation requirement</option><option value="best_effort">Cite when evidence is available</option><option value="required">Require citations</option></select></label><label>Consequential actions<select v-model="actionPolicy"><option value="none">Cannot propose actions</option><option value="propose">May propose selected actions for human approval</option></select></label><div v-if="actionPolicy === 'propose'" class="persona-choice-grid"><label v-for="item in actions" :key="item.capability"><input type="checkbox" :checked="actionCapabilities.includes(item.capability)" @change="toggleAction(item.capability)"><span>{{ item.label }}</span></label></div><p v-if="actionPolicy === 'propose'">A proposal never executes directly. The exact request must still be approved in Your Turn.</p></fieldset>
      <fieldset><legend>Account tools</legend><p>Grant only the data and additive drafting capabilities this Persona needs.</p><div class="persona-choice-grid"><label v-for="item in tools" :key="item.capability"><input type="checkbox" :checked="toolCapabilities.includes(item.capability)" @change="toggleTool(item.capability)"><span><strong>{{ item.label }}</strong><small>{{ item.description }}</small></span></label></div></fieldset>
      <details :open="advancedOpen" @toggle="toggleAdvanced"><summary>Model limits and budget</summary><div class="persona-advanced"><label>Provider code<input v-model="provider" pattern="[a-z][a-z0-9._:\-]{0,127}" required></label><label>Primary model<input v-model="model" pattern="[a-z][a-z0-9._:\-]{0,127}" required></label><label>Fallback models<input v-model="fallbackModels" placeholder="Up to two, separated by commas"></label><label>Reasoning effort<input v-model="reasoningEffort" pattern="[a-z][a-z0-9._:\-]{0,127}"></label><label>Maximum input tokens<input v-model.number="maximumInputTokens" type="number" min="1" max="2000000" required></label><label>Maximum output tokens<input v-model.number="maximumOutputTokens" type="number" min="1" max="32768" required></label><label>Maximum cost per turn (USD)<input v-model.number="maximumCostDollars" type="number" min="0" max="1000" step="0.01" required></label><label>Maximum tool steps<input v-model.number="maximumToolSteps" type="number" min="0" max="5" required></label></div></details>
      <p v-if="error" class="form-error" role="alert">{{ error }}</p><div class="modal-actions"><IoButton type="button" kind="secondary" @click="emit('close')">Back</IoButton><IoButton type="submit" :disabled="saving">{{ saving ? "Publishing…" : props.persona ? "Publish new version" : "Publish Persona" }}</IoButton></div>
    </form>
  </div>
</template>
