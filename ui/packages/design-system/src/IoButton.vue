<script setup lang="ts">
withDefaults(defineProps<{
  kind?: "primary" | "secondary" | "quiet";
  type?: "button" | "submit" | "reset";
  disabled?: boolean;
}>(), { kind: "primary", type: "button", disabled: false });
</script>

<template>
  <button class="io-button" :class="`io-button--${kind}`" :type="type" :disabled="disabled">
    <slot />
  </button>
</template>
