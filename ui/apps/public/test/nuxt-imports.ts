export function useRoute(): never {
  throw new Error("useRoute must be mocked by the component test");
}

export function useRuntimeConfig(): never {
  throw new Error("useRuntimeConfig must be mocked by the component test");
}

export function useState(): never {
  throw new Error("useState must be mocked by the composable test");
}
