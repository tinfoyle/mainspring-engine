<script setup lang="ts">
withDefaults(defineProps<{ compact?: boolean }>(), { compact: false });
</script>

<template>
  <span class="io-logo" aria-label="Infinite Ocean Spyglass">
    <span class="io-logo__mark" aria-hidden="true"><i /><i /><i /></span>
    <span v-if="!compact" class="io-logo__type"><strong>Infinite Ocean</strong><small>Spyglass</small></span>
  </span>
</template>
