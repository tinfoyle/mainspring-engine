import { onBeforeUnmount, onMounted, toValue, type MaybeRefOrGetter } from "vue";
import { useRouter } from "vue-router";

type NavigationBlockReason = "pending" | "unsaved";

interface SafeNavigationOptions {
  readonly dirty: MaybeRefOrGetter<boolean>;
  readonly pending: MaybeRefOrGetter<boolean>;
  readonly message?: string;
  readonly onBlocked?: (reason: NavigationBlockReason) => void;
}

export function useSafeNavigation(options: SafeNavigationOptions): { allowNextNavigation: () => void } {
  const router = useRouter();
  let allowOnce = false;
  let removeNavigationGuard: (() => void) | undefined;

  function protectedState(): boolean {
    return toValue(options.dirty) || toValue(options.pending);
  }

  function beforeUnload(event: BeforeUnloadEvent): void {
    if (!protectedState()) return;
    event.preventDefault();
    event.returnValue = "";
  }

  function canNavigate(): boolean {
    if (allowOnce) {
      allowOnce = false;
      return true;
    }
    if (toValue(options.pending)) {
      options.onBlocked?.("pending");
      return false;
    }
    if (!toValue(options.dirty)) return true;
    const leave = window.confirm(options.message ?? "Leave this page? Your unsaved work will remain only in this browser tab.");
    if (!leave) options.onBlocked?.("unsaved");
    return leave;
  }

  onMounted(() => {
    window.addEventListener("beforeunload", beforeUnload);
    removeNavigationGuard = router.beforeEach(canNavigate);
  });
  onBeforeUnmount(() => {
    window.removeEventListener("beforeunload", beforeUnload);
    removeNavigationGuard?.();
  });

  return {
    allowNextNavigation(): void {
      allowOnce = true;
    }
  };
}
