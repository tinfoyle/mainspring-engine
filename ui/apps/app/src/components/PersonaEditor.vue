<script setup lang="ts">
import { agentToolCatalog, getPublicCatalog, type AIComplexity, type AIComplexityRate, type AgentPersona, type AgentToolGrantInput, type PublishAgentPersonaRequest } from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, onMounted, ref, watch } from "vue";

type ToolCapability = AgentToolGrantInput["capability"];
const tools = agentToolCatalog.tools;
const actions = agentToolCatalog.actions;

const props = defineProps<{ persona?: AgentPersona | undefined; saving: boolean; error?: string | undefined }>();
const emit = defineEmits<{ close: []; publish: [input: PublishAgentPersonaRequest]; "dirty-change": [dirty: boolean] }>();
const name = ref(props.persona?.name ?? ""); const role = ref(props.persona?.role ?? ""); const description = ref(props.persona?.description ?? ""); const instructions = ref(props.persona?.system_instructions ?? "");
const complexityCodes = ["simple", "efficient", "balanced", "thorough", "advanced"] as const satisfies ReadonlyArray<AIComplexity>;
const existingComplexity = complexityCodes.includes(props.persona?.policy.complexity as AIComplexity) ? props.persona?.policy.complexity as AIComplexity : "balanced";
const complexityIndex = ref(complexityCodes.indexOf(existingComplexity));
const complexity = computed(() => complexityCodes[complexityIndex.value] ?? "balanced");
const complexityRates = ref<ReadonlyArray<AIComplexityRate>>([]);
const selectedRate = computed(() => complexityRates.value.find((rate) => rate.complexity === complexity.value));
const maximumInputTokens = ref(props.persona?.policy.maximum_input_tokens ?? 128000); const maximumOutputTokens = ref(props.persona?.policy.maximum_output_tokens ?? 4096); const maximumCostMicros = ref(props.persona?.policy.maximum_cost_micros ?? 500000); const maximumToolSteps = ref(props.persona?.policy.maximum_tool_steps ?? 5);
const citationPolicy = ref<"none" | "required" | "best_effort">(props.persona?.policy.citation_policy ?? "best_effort"); const actionPolicy = ref<"none" | "propose">(props.persona?.policy.action_policy ?? "none");
const toolCapabilities = ref<ToolCapability[]>(props.persona?.policy.tools.map((item) => item.capability) ?? []); const actionCapabilities = ref<string[]>([...(props.persona?.policy.action_capabilities ?? [])]);
const title = computed(() => props.persona ? `Publish ${props.persona.name} version ${props.persona.latest_version + 1}` : "Create an agent");
const complexityLabel = computed(() => complexity.value.replace(/^./, (first) => first.toUpperCase()));
const complexityEstimate = computed(() => selectedRate.value ? `${selectedRate.value.estimated_minimum.toLocaleString()}–${selectedRate.value.estimated_maximum.toLocaleString()} AI Tokens per turn` : "Current estimate unavailable");
function snapshot(): string {
  return JSON.stringify({
    name: name.value,
    role: role.value,
    description: description.value,
    instructions: instructions.value,
    complexity: complexity.value,
    maximumInputTokens: maximumInputTokens.value,
    maximumOutputTokens: maximumOutputTokens.value,
    maximumCostMicros: maximumCostMicros.value,
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
function publish(): void {
  const selectedTools: AgentToolGrantInput[] = toolCapabilities.value.map((capability) => { const value = tools.find((item) => item.capability === capability)!; return { name: value.name, capability: value.capability, description: value.description, input_schema: value.input_schema }; });
  emit("publish", {
    persona_id: props.persona?.id ?? crypto.randomUUID(), expected_latest_version: props.persona?.latest_version ?? 0,
    name: name.value.trim(), role: role.value.trim(), description: description.value.trim(), system_instructions: instructions.value.trim(),
    policy: { complexity: complexity.value, maximum_input_tokens: maximumInputTokens.value, maximum_output_tokens: maximumOutputTokens.value, maximum_cost_micros: maximumCostMicros.value, maximum_tool_steps: selectedTools.length ? Math.max(1, maximumToolSteps.value) : 0, citation_policy: citationPolicy.value, action_policy: actionPolicy.value, ...(actionPolicy.value === "propose" && actionCapabilities.value.length ? { action_capabilities: actionCapabilities.value } : {}), tools: selectedTools }
  });
}
onMounted(async () => { try { complexityRates.value = (await getPublicCatalog()).ai_complexity_rates; } catch { complexityRates.value = []; } });
</script>

<template>
  <div class="modal-backdrop">
    <form class="modal-card persona-modal" role="dialog" aria-modal="true" aria-labelledby="persona-editor-title" @submit.prevent="publish">
      <div class="persona-modal-heading"><div><h2 id="persona-editor-title">{{ title }}</h2></div><button type="button" aria-label="Close agent editor" @click="emit('close')">×</button></div>
      <label>Name<input v-model="name" minlength="2" maxlength="120" required></label><label>Role<input v-model="role" minlength="2" maxlength="160" required></label><label>Description<textarea v-model="description" maxlength="4000" rows="3"></textarea></label><label>System instructions<textarea v-model="instructions" minlength="20" maxlength="32768" rows="7" required></textarea><small>Describe what this agent should do and the rules it should follow.</small></label>
      <fieldset class="persona-complexity"><legend>Thinking complexity</legend><p>Choose how much time and detail the agent should use.</p><label for="persona-complexity">{{ complexityLabel }}<input id="persona-complexity" v-model.number="complexityIndex" type="range" min="0" max="4" step="1" :aria-valuetext="`${complexityLabel}: ${complexityEstimate}`"></label><div class="persona-complexity-labels" aria-hidden="true"><span v-for="code in complexityCodes" :key="code">{{ code }}</span></div><output for="persona-complexity" aria-label="Estimated AI Token usage"><strong>{{ complexityLabel }}</strong> · {{ complexityEstimate }}</output><small>Changes apply to future tasks.</small></fieldset>
      <fieldset><legend>Evidence and action policy</legend><label>Citations<select v-model="citationPolicy"><option value="none">No citation requirement</option><option value="best_effort">Cite when evidence is available</option><option value="required">Require citations</option></select></label><label>Actions requiring approval<select v-model="actionPolicy"><option value="none">Cannot propose actions</option><option value="propose">May propose selected actions for human approval</option></select></label><div v-if="actionPolicy === 'propose'" class="persona-choice-grid"><label v-for="item in actions" :key="item.capability"><input type="checkbox" :checked="actionCapabilities.includes(item.capability)" @change="toggleAction(item.capability)"><span>{{ item.label }}</span></label></div><p v-if="actionPolicy === 'propose'">A proposal never executes directly. The exact request must still be approved in Your Turn.</p></fieldset>
      <fieldset><legend>Account tools</legend><label>Maximum tool calls per turn<input v-model.number="maximumToolSteps" type="number" min="1" max="5"></label><p>Choose the information and tools this agent can use.</p><div class="persona-choice-grid"><label v-for="item in tools" :key="item.capability"><input type="checkbox" :checked="toolCapabilities.includes(item.capability)" @change="toggleTool(item.capability)"><span><strong>{{ item.label }}</strong><small>{{ item.description }}</small></span></label></div></fieldset>
      <p v-if="error" class="form-error" role="alert">{{ error }}</p><div class="modal-actions"><IoButton type="button" kind="secondary" @click="emit('close')">Back</IoButton><IoButton type="submit" :disabled="saving">{{ saving ? "Publishing…" : props.persona ? "Save new version" : "Create agent" }}</IoButton></div>
    </form>
  </div>
</template>
