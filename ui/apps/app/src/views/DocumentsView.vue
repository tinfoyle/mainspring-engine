<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { APIProblem, listDocuments, getDocument, documentPassages, searchDocuments, uploadDocument, publishDocument, deleteDocument,
  type KnowledgeDocumentSummary, type KnowledgeDocumentDetail, type KnowledgeDocumentCitation, type KnowledgeSensitivity } from "@spyglass/api";
import { useSessionStore } from "../stores/session";
import { useConversationStore } from "../stores/conversation";
import { useSafeNavigation } from "../composables/useSafeNavigation";
const session = useSessionStore(), chat = useConversationStore(), route = useRoute(), router = useRouter();
const items = ref<ReadonlyArray<KnowledgeDocumentSummary>>([]), detail = ref<KnowledgeDocumentDetail>();
const passages = ref<ReadonlyArray<KnowledgeDocumentCitation>>([]), results = ref<ReadonlyArray<KnowledgeDocumentCitation>>([]);
const loading = ref(false), saving = ref(false), error = ref(""), notice = ref("");
const cursor = ref(""), previous = ref<string[]>([]), nextCursor = ref("");
const titleFilter = ref(""), search = ref(""), uploadOpen = ref(false), title = ref("");
const sensitivity = ref<KnowledgeSensitivity>("internal"), file = ref<File>();
const operationID = ref(""), deleteOpen = ref(false), confirmation = ref("");
const morePassages = ref(false);
let sequence = 0, timer: ReturnType<typeof setTimeout> | undefined;
const id = computed(() => typeof route.params.documentID === "string" ? route.params.documentID : "");
const available = computed(() => session.selected?.entitlements.packages.some(p => p.code === "knowledge" && p.mode !== "suspended"));
const writable = computed(() => available.value && session.selected?.entitlements.packages.some(p => p.code === "knowledge" && p.mode === "enabled") &&
  !session.selected?.owner_enrollment_required && ["owner", "administrator", "member"].includes(session.selected?.role ?? ""));
const { allowNextNavigation } = useSafeNavigation({ dirty: computed(() => (uploadOpen.value && Boolean(title.value || file.value)) || (deleteOpen.value && Boolean(confirmation.value))), pending: saving });
const fail = (cause: unknown, fallback: string) => cause instanceof APIProblem ? cause.message : fallback;
const label = (value: string) => value.replaceAll("_", " ");
async function load(): Promise<void> {
  const ticket = ++sequence, accountID = session.selectedID;
  if (timer) clearTimeout(timer);
  if (!accountID || !available.value) { detail.value = undefined; items.value = []; passages.value = []; return; }
  loading.value = true; error.value = "";
  try {
    if (id.value) {
      const value = await getDocument(accountID, id.value);
      if (ticket !== sequence) return;
      detail.value = value; passages.value = []; morePassages.value = false;
      if (value.document.state === "ready" && value.document.current_revision_id) {
        const page = await documentPassages(accountID, value.document.id, value.document.current_revision_id);
        if (ticket !== sequence) return;
        passages.value = page.items; morePassages.value = page.items.length === 20;
      }
      if (value.document.state === "processing") timer = setTimeout(() => void load(), 5000);
    } else {
      const page = await listDocuments(accountID, { cursor: cursor.value, title: titleFilter.value.trim() });
      if (ticket !== sequence) return;
      items.value = page.items; nextCursor.value = page.next_cursor ?? "";
    }
  } catch (cause) { if (ticket === sequence) error.value = fail(cause, "Documents could not load. Try again."); }
  finally { if (ticket === sequence) loading.value = false; }
}
async function nextPassages(): Promise<void> {
  if (!session.selectedID || !detail.value?.document.current_revision_id || loading.value) return;
  const ticket = sequence, document = detail.value.document;
  loading.value = true;
  try {
    const page = await documentPassages(session.selectedID, document.id, document.current_revision_id!, passages.value.at(-1)?.chunk_index);
    if (ticket !== sequence) return;
    passages.value = [...passages.value, ...page.items]; morePassages.value = page.items.length === 20;
  } catch (cause) { if (ticket === sequence) error.value = fail(cause, "More passages could not load."); }
  finally { if (ticket === sequence) loading.value = false; }
}
function chooseFile(event: Event): void {
  file.value = (event.target as HTMLInputElement).files?.[0]; operationID.value = "";
  if (!title.value && file.value) title.value = file.value.name;
}
async function upload(): Promise<void> {
  if (!session.selectedID || !file.value || !writable.value || saving.value) return;
  if (!file.value.size || file.value.size > 50 * 1024 * 1024) { error.value = "Choose a file between 1 byte and 50 MB."; return; }
  saving.value = true; error.value = ""; const ticket = sequence;
  operationID.value ||= crypto.randomUUID();
  try {
    const value = await uploadDocument(session.selectedID, file.value, title.value.trim(), sensitivity.value, operationID.value);
    if (ticket !== sequence) return;
    uploadOpen.value = false; file.value = undefined; title.value = ""; operationID.value = "";
    notice.value = "Uploaded. The document is being checked and prepared."; allowNextNavigation();
    await router.push("/app/documents/" + value.document.id);
  } catch (cause) { if (ticket === sequence) error.value = fail(cause, "The upload could not be confirmed. Keep the same file and try again."); }
  finally { saving.value = false; }
}
async function publish(): Promise<void> {
  if (!session.selectedID || !detail.value || !writable.value || saving.value) return;
  saving.value = true; error.value = ""; operationID.value ||= crypto.randomUUID();
  try { await publishDocument(session.selectedID, detail.value, operationID.value); operationID.value = ""; notice.value = "Document published."; await load(); }
  catch (cause) { error.value = fail(cause, "Publication could not be confirmed. Refresh before trying again."); }
  finally { saving.value = false; }
}
async function remove(): Promise<void> {
  if (!session.selectedID || !detail.value || !writable.value || saving.value || confirmation.value !== detail.value.document.title) return;
  saving.value = true; error.value = ""; operationID.value ||= crypto.randomUUID();
  try { await deleteDocument(session.selectedID, detail.value.document, operationID.value); operationID.value = ""; deleteOpen.value = false; notice.value = "Deletion requested."; await load(); }
  catch (cause) { error.value = fail(cause, "Deletion could not be confirmed."); }
  finally { saving.value = false; }
}
async function find(): Promise<void> {
  if (!session.selectedID || !search.value.trim() || !available.value || loading.value) return;
  const ticket = sequence; loading.value = true; error.value = "";
  try { const page = await searchDocuments(session.selectedID, search.value.trim()); if (ticket === sequence) { results.value = page.items; notice.value = page.items.length ? "" : "No published passages matched."; } }
  catch (cause) { if (ticket === sequence) error.value = fail(cause, "Document search could not complete."); }
  finally { if (ticket === sequence) loading.value = false; }
}
function attach(passage: KnowledgeDocumentCitation): void {
  chat.attach({ kind: "document", id: passage.document_id + ":" + passage.chunk_id, title: passage.document_title,
    version: "revision " + passage.revision + ", passage " + (passage.chunk_index + 1),
    text: JSON.stringify({ content: passage.content, document_id: passage.document_id, revision_id: passage.revision_id, chunk_id: passage.chunk_id, content_sha256: passage.content_sha256 }) });
}
watch(() => [session.selectedID, id.value, available.value], () => {
  sequence++; detail.value = undefined; passages.value = []; results.value = []; items.value = [];
  cursor.value = ""; previous.value = []; nextCursor.value = ""; operationID.value = ""; deleteOpen.value = false;
  void load();
}, { immediate: true });
onBeforeUnmount(() => { sequence++; if (timer) clearTimeout(timer); });
</script>
<template>
  <section class="page documents-page">
    <header class="page-heading page-heading--action"><h1>{{ detail?.document.title || "Documents" }}</h1><button type="button" :disabled="loading" @click="load">Refresh</button></header>
    <p v-if="notice" role="status">{{ notice }}</p><p v-if="error" role="alert" class="chat-error">{{ error }}</p>
    <p v-if="!available">Documents are not included in this account’s current access. <RouterLink to="/app/billing">Open billing</RouterLink></p>
    <template v-else-if="id">
      <RouterLink to="/app/documents">← All documents</RouterLink>
      <p v-if="loading && !detail" role="status">Opening document…</p>
      <template v-if="detail">
        <p class="document-status">{{ label(detail.document.state) }} · {{ detail.latest_revision.filename }} · {{ label(detail.document.sensitivity) }}</p>
        <p v-if="detail.document.state === 'processing'" role="status">Checking and preparing this document. This view updates automatically.</p>
        <p v-if="detail.latest_revision.state === 'failed'">This file could not be prepared. <span>{{ label(detail.latest_revision.failure_code) }}</span></p>
        <button v-if="writable && detail.latest_revision.state === 'ready' && detail.document.current_revision_id !== detail.latest_revision.id" type="button" :disabled="saving" @click="publish">Publish prepared document</button>
        <div v-if="passages.length" class="document-passages">
          <article v-for="passage in passages" :key="passage.chunk_id" class="document-passage"><header><span>Revision {{ passage.revision }} · Passage {{ passage.chunk_index + 1 }}</span><button v-if="chat.available" type="button" @click="attach(passage)">Use passage in chat</button></header><pre>{{ passage.content }}</pre></article>
          <button v-if="morePassages" type="button" :disabled="loading" @click="nextPassages">Show more passages</button>
        </div>
        <p v-else-if="detail.document.state === 'ready' && !loading">No published text is available for this revision.</p>
        <details class="document-metadata"><summary>File details</summary><dl><div><dt>Revision</dt><dd>{{ detail.latest_revision.number }}</dd></div><div><dt>Size</dt><dd>{{ (detail.latest_revision.byte_size / 1024).toFixed(1) }} KB</dd></div><div><dt>Scan</dt><dd>{{ label(detail.latest_revision.scan_state) }}</dd></div><div><dt>Updated</dt><dd>{{ new Date(detail.document.updated_at).toLocaleString() }}</dd></div></dl></details>
        <button v-if="writable && !['deleted','deletion_pending'].includes(detail.document.state)" type="button" :disabled="saving" @click="deleteOpen = !deleteOpen">Delete document…</button>
        <form v-if="deleteOpen" class="document-upload" @submit.prevent="remove"><p>Request deletion of this document. Retention requirements may prevent deletion.</p><label>Type {{ detail.document.title }} to confirm<input v-model="confirmation" required></label><button type="submit" :disabled="saving || confirmation !== detail.document.title">Request deletion</button><button type="button" :disabled="saving" @click="deleteOpen = false">Cancel</button></form>
      </template>
    </template>
    <template v-else>
      <div class="document-library-tools"><form @submit.prevent="cursor = ''; previous = []; load()"><label>Find by title<input v-model="titleFilter" maxlength="240" type="search"></label><button type="submit" :disabled="loading">Find</button></form><button v-if="writable" type="button" :disabled="saving" @click="uploadOpen = !uploadOpen">{{ uploadOpen ? "Close upload" : "Upload document" }}</button></div>
      <form v-if="uploadOpen" class="document-upload" @submit.prevent="upload">
        <label>File<input type="file" :disabled="saving" accept=".txt,.log,.md,.markdown,.csv,.tsv,.json,.xml,.html,.htm,.yaml,.yml,.pdf,.docx" required @change="chooseFile"></label>
        <label>Title<input v-model="title" :disabled="saving" maxlength="240" required @input="operationID = ''"></label>
        <label>Access<select v-model="sensitivity" :disabled="saving" @change="operationID = ''"><option value="internal">Account members</option><option v-if="['owner', 'administrator'].includes(session.selected?.role ?? '')" value="restricted">Owners and administrators</option></select></label>
        <p class="chat-note">Up to 50 MB. Files are checked before their text becomes available.</p><button type="submit" :disabled="saving">{{ saving ? "Uploading…" : "Upload" }}</button>
      </form>
      <p v-if="loading && !items.length" role="status">Loading documents…</p>
      <p v-else-if="!items.length">No documents match this view.</p>
      <ol class="document-list"><li v-for="item in items" :key="item.id"><RouterLink :to="'/app/documents/' + item.id"><strong>{{ item.title }}</strong><span>{{ label(item.state) }} · Revision {{ item.current_revision || 1 }}</span></RouterLink></li></ol>
      <nav v-if="previous.length || nextCursor" class="document-pagination" aria-label="Document pages"><button type="button" :disabled="!previous.length || loading" @click="cursor = previous.pop() ?? ''; load()">Previous</button><button type="button" :disabled="!nextCursor || loading" @click="previous.push(cursor); cursor = nextCursor; load()">Next</button></nav>
      <details class="document-search"><summary>Search inside published documents</summary><form @submit.prevent="find"><label>Words or phrase<input v-model="search" maxlength="512" required></label><button type="submit" :disabled="loading">Search text</button></form><article v-for="passage in results" :key="passage.chunk_id" class="document-passage"><RouterLink :to="'/app/documents/' + passage.document_id">{{ passage.document_title }}</RouterLink><pre>{{ passage.content }}</pre><button v-if="chat.available" type="button" @click="attach(passage)">Use passage in chat</button></article></details>
    </template>
  </section>
</template>
