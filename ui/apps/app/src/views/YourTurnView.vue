<script setup lang="ts">
import { emitAnalytics } from "@spyglass/api";
import { IoButton } from "@spyglass/design-system";
import { onMounted } from "vue";

onMounted(() => void emitAnalytics(false, { name: "your_turn_opened", fields: { queue_state: "placeholder" } }));
</script>

<template>
  <section class="page your-turn">
    <header class="page-heading">
      <p class="eyebrow">Monday · Your workspace</p>
      <h1>Your Turn</h1>
      <p>Decisions, answers, and approvals that need you—nothing else competing for attention.</p>
    </header>

    <div class="queue-toolbar" aria-label="Queue controls">
      <button type="button" class="filter-chip filter-chip--active">Needs action <strong>3</strong></button>
      <button type="button" class="filter-chip">Waiting</button>
      <button type="button" class="filter-chip">All</button>
      <button type="button" class="filter-button">Filters <span aria-hidden="true">⌄</span></button>
    </div>

    <ol class="attention-list" aria-label="Items needing your attention">
      <li>
        <article class="attention-card attention-card--urgent">
          <div class="card-meta"><span>Approval</span><time>18 min ago</time></div>
          <h2>Approve the August campaign launch</h2>
          <p>The Marketing team is ready to publish. Review the final channels and audience before anything goes live.</p>
          <footer><span class="account-dot">Infinite Ocean</span><IoButton>Review approval</IoButton></footer>
        </article>
      </li>
      <li>
        <article class="attention-card">
          <div class="card-meta"><span>Information</span><time>2 hr ago</time></div>
          <h2>What is our standard refund window?</h2>
          <p>Finance needs one policy detail before it can finish the customer billing baseline.</p>
          <footer><span class="account-dot">Infinite Ocean</span><IoButton kind="secondary">Answer question</IoButton></footer>
        </article>
      </li>
      <li>
        <article class="attention-card">
          <div class="card-meta"><span>Work review</span><time>Yesterday</time></div>
          <h2>Review the onboarding email sequence</h2>
          <p>An Agent prepared three drafts and cited the approved messaging brief.</p>
          <footer><span class="account-dot">Infinite Ocean</span><IoButton kind="secondary">Open review</IoButton></footer>
        </article>
      </li>
    </ol>
  </section>
</template>
