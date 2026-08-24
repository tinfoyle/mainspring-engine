<script setup lang="ts">
import { useRoute } from "vue-router";
const route = useRoute();
</script>

<template>
  <section class="page">
    <header class="page-heading">
      <p class="eyebrow">Vue migration boundary</p>
      <h1>{{ String(route.meta.title) }}</h1>
      <p>This feature is entering the new mobile-first shell after the complete Your Turn vertical slice.</p>
    </header>
  </section>
</template>
