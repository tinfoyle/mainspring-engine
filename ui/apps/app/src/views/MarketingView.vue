<script setup lang="ts">
import {
  APIProblem, activateMarketingCampaign, archiveMarketingCampaign, cancelMarketingRelease, completeMarketingCampaign,
  createMarketingCampaign, createMarketingRelease, getMarketingCampaign, getMarketingRelease, listMarketingAssetRevisions,
  listMarketingCampaigns, listMarketingReleases, pauseMarketingCampaign, reviseMarketingCampaign, submitMarketingRelease,
  uploadMarketingAssetRevision,
  type MarketingAssetKind, type MarketingAssetRevision, type MarketingCampaign, type MarketingCampaignState, type MarketingChannel,
  type MarketingRelease
} from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { computed, ref, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { useSafeNavigation } from "../composables/useSafeNavigation";
import { useSessionStore } from "../stores/session";

type Tab = "overview" | "creative" | "releases";
type Action = "campaign-create" | "campaign-edit" | "campaign-archive" | "asset-upload" | "release-create" | "release-submit" | "release-cancel" | "campaign-activate" | "campaign-pause" | "campaign-complete";
const session = useSessionStore(); const route = useRoute(); const router = useRouter();
const campaigns = ref<ReadonlyArray<MarketingCampaign>>([]); const selectedCampaign = ref<MarketingCampaign>(); const assets = ref<ReadonlyArray<MarketingAssetRevision>>([]); const releases = ref<ReadonlyArray<MarketingRelease>>([]); const selectedRelease = ref<MarketingRelease>();
const campaignCursor = ref<string>(); const assetCursor = ref<string>(); const releaseCursor = ref<string>(); const campaignState = ref<"" | MarketingCampaignState>(""); const tab = ref<Tab>("overview");
const loading = ref(false); const loadingMore = ref(false); const saving = ref(false); const error = ref(""); const announcement = ref(""); const navigationNotice = ref(""); let sequence = 0;
const modalOpen = ref(false); const action = ref<Action>("campaign-create"); const confirmation = ref("");
const name = ref(""); const objective = ref(""); const audience = ref(""); const channels = ref<MarketingChannel[]>(["web"]);
const assetID = ref(""); const assetKind = ref<MarketingAssetKind>("copy"); const assetTitle = ref(""); const mediaType = ref(""); const alternativeText = ref(""); const assetFile = ref<File>(); const selectedAssetIDs = ref<string[]>([]);

const marketingPackage = computed(() => session.selected?.entitlements.packages.find((item) => item.code === "marketing"));
const available = computed(() => Boolean(marketingPackage.value && marketingPackage.value.mode !== "suspended"));
const writable = computed(() => marketingPackage.value?.mode === "enabled" && ["owner", "administrator", "member"].includes(session.selected?.role ?? ""));
const manageable = computed(() => marketingPackage.value?.mode === "enabled" && ["owner", "administrator"].includes(session.selected?.role ?? ""));
const campaignEditable = computed(() => selectedCampaign.value && ["draft", "paused"].includes(selectedCampaign.value.state));
const latestAssets = computed(() => assets.value.filter((item, index, all) => all.findIndex((candidate) => candidate.asset_id === item.asset_id) === index));
const releaseCurrent = computed(() => selectedRelease.value && selectedCampaign.value && selectedRelease.value.campaign_version === selectedCampaign.value.version);
const releaseActivatable = computed(() => selectedRelease.value?.state === "approved" && campaignEditable.value && releaseCurrent.value && selectedRelease.value.channels.join("|") === selectedCampaign.value?.channels.join("|"));
const formValid = computed(() => {
  if (["campaign-create", "campaign-edit"].includes(action.value)) return Boolean(name.value.trim() && objective.value.trim() && audience.value.trim() && channels.value.length);
  if (action.value === "asset-upload") return Boolean(assetID.value && assetTitle.value.trim() && mediaType.value.trim() && assetFile.value && (assetKind.value !== "image" || alternativeText.value.trim()));
  if (action.value === "release-create") return Boolean(name.value.trim() && selectedAssetIDs.value.length);
  return !phrase() || confirmation.value === phrase();
});
const { allowNextNavigation } = useSafeNavigation({
  dirty: modalOpen,
  pending: saving,
  message: "Leave Marketing? Your open campaign or release command will be lost.",
  onBlocked: (blockedReason) => {
    navigationNotice.value = blockedReason === "pending"
      ? "This Marketing change is still being saved. Stay on this page until Spyglass confirms the result."
      : "Navigation canceled. Your Marketing command remains open.";
  }
});

function label(value: string): string { return value.replaceAll("_", " ").replace(/^./, (first) => first.toUpperCase()); }
function date(value?: string): string { return value ? new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(value)) : "Not set"; }
function short(value?: string): string { return value ? `${value.slice(0, 12)}…` : "Not set"; }
function assetDownload(revisionID: string): string { return `/api/v1/accounts/${encodeURIComponent(session.selectedID ?? "")}/marketing/campaigns/${encodeURIComponent(selectedCampaign.value?.id ?? "")}/asset-revisions/${encodeURIComponent(revisionID)}/content`; }
function bytes(value: number): string { return value < 1024 ? `${value} B` : value < 1024 * 1024 ? `${(value / 1024).toFixed(1)} KiB` : `${(value / 1024 / 1024).toFixed(1)} MiB`; }
function target(): { kind?: "campaign" | "release"; id?: string } {
  if (typeof route.params.campaignID === "string") return { kind: "campaign", id: route.params.campaignID };
  if (typeof route.params.releaseID === "string") return { kind: "release", id: route.params.releaseID };
  return {};
}
async function loadCampaign(accountID: string, campaignID: string, releaseID?: string): Promise<void> {
  const [campaign, assetPage, releasePage] = await Promise.all([getMarketingCampaign(accountID, campaignID), listMarketingAssetRevisions(accountID, campaignID), listMarketingReleases(accountID, campaignID)]);
  selectedCampaign.value = campaign; assets.value = assetPage.items; assetCursor.value = assetPage.next_cursor; releases.value = releasePage.items; releaseCursor.value = releasePage.next_cursor;
  if (releaseID) { selectedRelease.value = releasePage.items.find((item) => item.id === releaseID) ?? await getMarketingRelease(accountID, releaseID); tab.value = "releases"; }
  else if (selectedRelease.value?.campaign_id !== campaignID) selectedRelease.value = undefined;
}
async function load(): Promise<void> {
  const accountID = session.selectedID; const current = ++sequence; campaigns.value = []; selectedCampaign.value = undefined; selectedRelease.value = undefined; assets.value = []; releases.value = []; campaignCursor.value = undefined; assetCursor.value = undefined; releaseCursor.value = undefined; error.value = "";
  if (!accountID || !available.value) return; loading.value = true;
  try {
    const page = await listMarketingCampaigns(accountID, { state: campaignState.value || undefined }); if (current !== sequence) return;
    campaigns.value = page.items; campaignCursor.value = page.next_cursor; const routeTarget = target();
    if (routeTarget.kind === "release" && routeTarget.id) { const release = await getMarketingRelease(accountID, routeTarget.id); await loadCampaign(accountID, release.campaign_id, release.id); }
    else if (routeTarget.kind === "campaign" && routeTarget.id) await loadCampaign(accountID, routeTarget.id);
    else if (page.items[0]) await loadCampaign(accountID, page.items[0].id);
  } catch (cause) { if (current === sequence) error.value = cause instanceof APIProblem ? cause.message : "Marketing is temporarily unavailable."; }
  finally { if (current === sequence) loading.value = false; }
}
async function loadMore(kind: "campaigns" | "assets" | "releases"): Promise<void> {
  const accountID = session.selectedID; if (!accountID || loadingMore.value) return; loadingMore.value = true;
  try {
    if (kind === "campaigns" && campaignCursor.value) { const page = await listMarketingCampaigns(accountID, { state: campaignState.value || undefined, cursor: campaignCursor.value }); campaigns.value = [...campaigns.value, ...page.items]; campaignCursor.value = page.next_cursor; }
    if (kind === "assets" && selectedCampaign.value && assetCursor.value) { const page = await listMarketingAssetRevisions(accountID, selectedCampaign.value.id, { cursor: assetCursor.value }); assets.value = [...assets.value, ...page.items]; assetCursor.value = page.next_cursor; }
    if (kind === "releases" && selectedCampaign.value && releaseCursor.value) { const page = await listMarketingReleases(accountID, selectedCampaign.value.id, releaseCursor.value); releases.value = [...releases.value, ...page.items]; releaseCursor.value = page.next_cursor; }
  } catch (cause) { error.value = cause instanceof APIProblem ? cause.message : "More Marketing records could not be loaded."; }
  finally { loadingMore.value = false; }
}
async function navigate(path: string): Promise<void> { if (route.path === path) await load(); else await router.push(path); }
function selectTab(value: Tab): void { tab.value = value; }
function resetForm(): void { confirmation.value = ""; error.value = ""; name.value = ""; objective.value = ""; audience.value = ""; channels.value = ["web"]; assetID.value = ""; assetKind.value = "copy"; assetTitle.value = ""; mediaType.value = ""; alternativeText.value = ""; assetFile.value = undefined; selectedAssetIDs.value = []; }
function begin(value: Action, release?: MarketingRelease): void {
  action.value = value; resetForm(); if (release) selectedRelease.value = release; const campaign = selectedCampaign.value;
  if (value === "campaign-edit" && campaign) { name.value = campaign.name; objective.value = campaign.objective; audience.value = campaign.audience; channels.value = [...campaign.channels]; }
  if (value === "asset-upload") { assetID.value = crypto.randomUUID(); }
  if (value === "release-create" && campaign) { channels.value = [...campaign.channels]; selectedAssetIDs.value = latestAssets.value.map((item) => item.id); }
  modalOpen.value = true;
}
function chooseExistingAsset(event: Event): void { const value = (event.target as HTMLSelectElement).value; assetID.value = value || crypto.randomUUID(); }
function selectFile(event: Event): void { const file = (event.target as HTMLInputElement).files?.[0]; assetFile.value = file; if (file?.type) mediaType.value = file.type; }
function toggleChannel(value: MarketingChannel): void { channels.value = channels.value.includes(value) ? channels.value.filter((item) => item !== value) : [...channels.value, value]; }
function toggleAsset(value: string): void { selectedAssetIDs.value = selectedAssetIDs.value.includes(value) ? selectedAssetIDs.value.filter((item) => item !== value) : [...selectedAssetIDs.value, value]; }
function phrase(): string { return ({ "campaign-archive": "ARCHIVE", "release-submit": "SUBMIT", "release-cancel": "CANCEL", "campaign-activate": "ACTIVATE", "campaign-pause": "PAUSE", "campaign-complete": "COMPLETE" } as Partial<Record<Action, string>>)[action.value] ?? ""; }
function title(): string { return ({ "campaign-create": "Create campaign", "campaign-edit": "Revise campaign", "campaign-archive": "Archive campaign", "asset-upload": "Add content", "release-create": "Create release", "release-submit": "Submit release", "release-cancel": "Cancel release", "campaign-activate": "Activate approved release", "campaign-pause": "Pause campaign", "campaign-complete": "Complete campaign" } as Record<Action, string>)[action.value]; }
async function submit(): Promise<void> {
  const accountID = session.selectedID; const campaign = selectedCampaign.value; const release = selectedRelease.value; if (!accountID || saving.value || !formValid.value) return; saving.value = true; error.value = ""; navigationNotice.value = "";
  try {
    let path = route.path;
    if (action.value === "campaign-create") { const value = await createMarketingCampaign(accountID, { name: name.value.trim(), objective: objective.value.trim(), audience: audience.value.trim(), channels: channels.value }); path = `/app/marketing/campaigns/${value.id}`; }
    else if (action.value === "campaign-edit" && campaign) await reviseMarketingCampaign(accountID, campaign, { name: name.value.trim(), objective: objective.value.trim(), audience: audience.value.trim(), channels: channels.value });
    else if (action.value === "campaign-archive" && campaign) { await archiveMarketingCampaign(accountID, campaign); path = "/app/marketing"; }
    else if (action.value === "asset-upload" && campaign && assetFile.value) await uploadMarketingAssetRevision(accountID, campaign.id, { assetID: assetID.value, kind: assetKind.value, title: assetTitle.value.trim(), mediaType: mediaType.value.trim(), ...(alternativeText.value.trim() ? { alternativeText: alternativeText.value.trim() } : {}), file: assetFile.value });
    else if (action.value === "release-create" && campaign) { const value = await createMarketingRelease(accountID, campaign.id, { campaign_version: campaign.version, name: name.value.trim(), channels: campaign.channels, asset_revision_ids: selectedAssetIDs.value }); path = `/app/marketing/releases/${value.id}`; }
    else if (action.value === "release-submit" && campaign && release) await submitMarketingRelease(accountID, release, { campaign_version: campaign.version });
    else if (action.value === "release-cancel" && release) await cancelMarketingRelease(accountID, release);
    else if (action.value === "campaign-activate" && campaign && release) await activateMarketingCampaign(accountID, campaign, release.id);
    else if (action.value === "campaign-pause" && campaign) await pauseMarketingCampaign(accountID, campaign);
    else if (action.value === "campaign-complete" && campaign) await completeMarketingCampaign(accountID, campaign);
    modalOpen.value = false; announcement.value = `${title()} completed.`; if (path !== route.path) allowNextNavigation(); await navigate(path);
  } catch (cause) { if (cause instanceof APIProblem && cause.status === 409) { modalOpen.value = false; await load(); error.value = "This Marketing record changed. Review the current version before trying again."; } else error.value = cause instanceof APIProblem ? cause.message : "The Marketing command could not be completed."; }
  finally { saving.value = false; }
}
watch(() => [session.selectedID, marketingPackage.value?.mode, route.path, campaignState.value], () => void load(), { immediate: true });
</script>

<template>
  <section class="page marketing-page">
    <p class="sr-only" aria-live="polite">{{ announcement }}</p>
    <p v-if="navigationNotice && !modalOpen" class="queue-inline-status" role="status">{{ navigationNotice }}</p>
    <header class="page-heading page-heading--action"><div><h1>Marketing</h1><p>Prepare campaign content and send it for approval.</p></div><span v-if="available" class="state-badge">{{ marketingPackage?.mode === "enabled" ? "Package enabled" : "Read-only access" }}</span></header>
    <section v-if="!session.selectedID" class="queue-state"><h2>Select an Account</h2><p>Marketing always belongs to one Account.</p></section>
    <section v-else-if="!available" class="queue-state"><h2>Marketing is not included</h2><p>This Account's current package set does not expose Marketing.</p><RouterLink to="/app/billing">Review Account billing</RouterLink></section>
    <template v-else>
      <p v-if="error" class="queue-inline-status queue-inline-status--error" role="alert">{{ error }}</p>
      <section class="marketing-toolbar"><label>Campaign state<select v-model="campaignState"><option value="">All states</option><option value="draft">Draft</option><option value="active">Active</option><option value="paused">Paused</option><option value="completed">Completed</option><option value="archived">Archived</option></select></label><div><IoButton v-if="campaignCursor" kind="secondary" :disabled="loadingMore" @click="loadMore('campaigns')">More campaigns</IoButton><IoButton kind="secondary" @click="load">Refresh</IoButton><IoButton v-if="writable" @click="begin('campaign-create')">New campaign</IoButton></div></section>
      <div v-if="loading" class="queue-state" role="status">Loading Marketing workspace…</div>
      <section v-else-if="campaigns.length === 0 && !selectedCampaign" class="queue-state"><h2>No campaigns yet</h2><p>A participant can define the first audience, objective and channel intent.</p></section>
      <template v-else>
        <section class="marketing-workspace">
          <div class="marketing-list-panel"><header><div><h2>Campaigns</h2></div></header><ol><li v-for="item in campaigns" :key="item.id"><button type="button" :aria-current="selectedCampaign?.id === item.id ? 'page' : undefined" @click="navigate(`/app/marketing/campaigns/${item.id}`)"><span><strong>{{ item.name }}</strong><small>{{ item.channels.map(label).join(" · ") }} · {{ date(item.updated_at) }}</small></span><em>{{ label(item.state) }}</em></button></li></ol></div>
          <div class="marketing-stage">
            <article v-if="selectedCampaign" class="marketing-detail"><div class="marketing-detail-heading"><div><h2>{{ selectedCampaign.name }}</h2></div><span class="state-badge">{{ label(selectedCampaign.state) }}</span></div><p>{{ selectedCampaign.objective }}</p><dl><div><dt>Audience</dt><dd>{{ selectedCampaign.audience }}</dd></div><div><dt>Channels</dt><dd>{{ selectedCampaign.channels.map(label).join(" · ") }}</dd></div><div><dt>Version</dt><dd>{{ selectedCampaign.version }}</dd></div><div><dt>Origin</dt><dd>{{ label(selectedCampaign.provenance.origin) }}</dd></div></dl><div class="marketing-actions"><IoButton v-if="writable && campaignEditable" kind="secondary" @click="begin('campaign-edit')">Edit campaign</IoButton><IoButton v-if="manageable && selectedCampaign.state === 'active'" kind="secondary" @click="begin('campaign-pause')">Pause</IoButton><IoButton v-if="manageable && ['active', 'paused'].includes(selectedCampaign.state)" kind="secondary" @click="begin('campaign-complete')">Complete</IoButton><IoButton v-if="manageable && !['active', 'archived'].includes(selectedCampaign.state)" kind="secondary" @click="begin('campaign-archive')">Archive</IoButton></div></article>
            <nav v-if="selectedCampaign" class="finance-tabs" aria-label="Marketing workspace"><button v-for="value in (['overview', 'creative', 'releases'] as const)" :key="value" type="button" :aria-current="tab === value ? 'page' : undefined" @click="selectTab(value)">{{ label(value) }}</button></nav>
            <section v-if="selectedCampaign && tab === 'overview'" class="marketing-overview"><article><strong>{{ assets.length }}</strong><span>saved version{{ assets.length === 1 ? "" : "s" }}</span><IoButton kind="quiet" @click="tab = 'creative'">Review creative</IoButton></article><article><strong>{{ releases.length }}</strong><span>release{{ releases.length === 1 ? "" : "s" }}</span><IoButton kind="quiet" @click="tab = 'releases'">Review releases</IoButton></article><aside><strong>Publishing is a separate step</strong><p>Approval does not send or publish the campaign. Use Integrations to arrange delivery.</p><RouterLink to="/app/integrations">Open Integrations →</RouterLink></aside></section>
            <section v-else-if="selectedCampaign && tab === 'creative'" class="marketing-collection"><header><div><h2>Content versions</h2></div><IoButton v-if="writable && campaignEditable" @click="begin('asset-upload')">Add content</IoButton></header><ol><li v-for="item in assets" :key="item.id"><div><strong>{{ item.title }}</strong><small>{{ label(item.kind) }} · revision {{ item.revision }} · {{ item.media_type }}</small><span>{{ bytes(item.content_bytes) }}</span><a :href="assetDownload(item.id)" download>Download {{ item.title }}</a><span v-if="item.alternative_text">Alt: {{ item.alternative_text }}</span></div><em>{{ label(item.provenance.origin) }}</em></li></ol><p v-if="assets.length === 0">No content has been added yet.</p><IoButton v-if="assetCursor" kind="secondary" :disabled="loadingMore" @click="loadMore('assets')">Load more revisions</IoButton></section>
            <section v-else-if="selectedCampaign && tab === 'releases'" class="marketing-releases"><header><div><h2>Releases</h2></div><IoButton v-if="writable && campaignEditable && assets.length" @click="begin('release-create')">Create release</IoButton></header><div class="marketing-release-layout"><ol><li v-for="item in releases" :key="item.id"><button type="button" :aria-current="selectedRelease?.id === item.id ? 'page' : undefined" @click="navigate(`/app/marketing/releases/${item.id}`)"><span><strong>{{ item.name }}</strong><small>Campaign v{{ item.campaign_version }} · {{ item.asset_revision_ids.length }} creative revision{{ item.asset_revision_ids.length === 1 ? "" : "s" }}</small></span><em>{{ label(item.state) }}</em></button></li></ol><article v-if="selectedRelease" class="marketing-release-detail"><div><h3>{{ selectedRelease.name }}</h3><span class="state-badge">{{ label(selectedRelease.state) }}</span></div><dl><div><dt>Campaign version</dt><dd>{{ selectedRelease.campaign_version }}{{ releaseCurrent ? " · current" : " · stale" }}</dd></div><div><dt>Channels</dt><dd>{{ selectedRelease.channels.map(label).join(" · ") }}</dd></div><div><dt>Creative</dt><dd>{{ selectedRelease.asset_revision_ids.length }} exact revision{{ selectedRelease.asset_revision_ids.length === 1 ? "" : "s" }}</dd></div><div><dt>Release version</dt><dd>{{ selectedRelease.version }}</dd></div><div v-if="selectedRelease.approval_id"><dt>Approval</dt><dd>{{ short(selectedRelease.approval_id) }}</dd></div></dl><ul aria-label="Release files"><li v-for="(revisionID, index) in selectedRelease.asset_revision_ids" :key="revisionID"><a :href="assetDownload(revisionID)" download>{{ assets.find((asset) => asset.id === revisionID)?.title ?? `Download file ${index + 1}` }}</a></li></ul><p v-if="selectedRelease.state === 'submitted'">Waiting for an owner or administrator to review this release in Your Turn.</p><p v-if="!releaseCurrent && selectedRelease.state === 'draft'" class="queue-inline-status">Campaign intent changed after this snapshot. Build a new release from the current campaign version.</p><div class="marketing-actions"><IoButton v-if="writable && ['draft', 'submitted'].includes(selectedRelease.state) && releaseCurrent" @click="begin('release-submit', selectedRelease)">Request approval</IoButton><RouterLink v-if="selectedRelease.state === 'submitted'" class="io-link-button" to="/app/your-turn">Open Your Turn</RouterLink><IoButton v-if="manageable && ['submitted', 'approved'].includes(selectedRelease.state)" kind="secondary" @click="begin('release-cancel', selectedRelease)">Cancel release</IoButton><IoButton v-if="manageable && releaseActivatable" @click="begin('campaign-activate', selectedRelease)">Activate release</IoButton></div></article><article v-else class="marketing-release-detail"><h3>Select a release</h3><p>Choose a release to review its content and approval status.</p></article></div><p v-if="releases.length === 0">No releases yet.</p><IoButton v-if="releaseCursor" kind="secondary" :disabled="loadingMore" @click="loadMore('releases')">Load more releases</IoButton></section>
          </div>
        </section>
      </template>
    </template>
    <div v-if="modalOpen" class="modal-backdrop">
      <form class="modal-card marketing-modal" role="dialog" aria-modal="true" aria-labelledby="marketing-modal-title" @submit.prevent="submit">
        <h2 id="marketing-modal-title">{{ title() }}</h2>
        <template v-if="['campaign-create', 'campaign-edit'].includes(action)"><label>Name<input v-model="name" maxlength="160" required></label><label>Objective<textarea v-model="objective" maxlength="4000" rows="3" required></textarea></label><label>Audience<textarea v-model="audience" maxlength="4000" rows="3" required></textarea></label><fieldset><legend>Channels</legend><label><input type="checkbox" :checked="channels.includes('email')" @change="toggleChannel('email')"> Email</label><label><input type="checkbox" :checked="channels.includes('web')" @change="toggleChannel('web')"> Web</label></fieldset></template>
        <template v-else-if="action === 'asset-upload'"><label>Creative item<select @change="chooseExistingAsset"><option value="">New creative item</option><option v-for="item in latestAssets" :key="item.asset_id" :value="item.asset_id">New revision of {{ item.title }}</option></select></label><label>Kind<select v-model="assetKind"><option value="copy">Copy</option><option value="image">Image</option><option value="document">Document</option></select></label><label>Title<input v-model="assetTitle" maxlength="240" required></label><label>Creative file<input type="file" required @change="selectFile"></label><label>Media type<input v-model="mediaType" maxlength="100" placeholder="Detected from the selected file" required></label><label>Alternative text<textarea v-model="alternativeText" maxlength="1000" rows="2" :required="assetKind === 'image'"></textarea><small>{{ assetKind === "image" ? "Required for images." : "Optional accessibility description." }}</small></label><p>Choose a file up to 16 MB.</p></template>
        <template v-else-if="action === 'release-create'"><label>Release name<input v-model="name" maxlength="160" required></label><p>This snapshot freezes campaign version {{ selectedCampaign?.version }} and channels {{ selectedCampaign?.channels.map(label).join(" · ") }}.</p><fieldset class="marketing-asset-picker"><legend>Content to include</legend><label v-for="item in assets" :key="item.id"><input type="checkbox" :checked="selectedAssetIDs.includes(item.id)" @change="toggleAsset(item.id)"><span><strong>{{ item.title }}</strong><small>{{ label(item.kind) }} · revision {{ item.revision }} · {{ short(item.content_sha256) }}</small></span></label></fieldset></template>
        <template v-else><p>{{ action === "release-submit" ? "An owner or administrator will review this version in Your Turn. Later edits need a new release." : action === "release-cancel" ? "Cancellation invalidates this submitted or approved snapshot. An active campaign must be paused first." : action === "campaign-activate" ? "Activation records approved intent only. It does not send email or publish to the web." : action === "campaign-pause" ? "Pausing prevents this campaign from remaining active while preserving its history." : action === "campaign-complete" ? "Completion closes the campaign lifecycle without deleting its evidence." : "Archived campaigns remain readable and cannot be active." }}</p></template>
        <label v-if="phrase()">Type {{ phrase() }} to confirm<input v-model="confirmation" autocomplete="off" :pattern="phrase()" required></label><p v-if="navigationNotice" class="queue-inline-status" role="status">{{ navigationNotice }}</p><p v-if="error" class="form-error" role="alert">{{ error }}</p><div class="modal-actions"><IoButton type="button" kind="secondary" @click="modalOpen = false">Back</IoButton><IoButton type="submit" :disabled="saving || !formValid">{{ saving ? "Saving…" : title() }}</IoButton></div>
      </form>
    </div>
  </section>
</template>
