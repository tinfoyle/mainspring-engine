export type ApplicationEntryPoint = "checkout" | "your_turn" | "deep_link";

export function applicationEntryPoint(routeName: unknown): ApplicationEntryPoint {
  if (routeName === "checkout") return "checkout";
  if (routeName === "your-turn" || routeName === "your-turn-detail") return "your_turn";
  return "deep_link";
}
