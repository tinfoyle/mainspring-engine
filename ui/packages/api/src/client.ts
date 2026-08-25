import type { Problem } from "./generated/api-types";

export type UnauthorizedHandler = (path: string) => void;
let unauthorizedHandler: UnauthorizedHandler | undefined;

export function setUnauthorizedHandler(handler?: UnauthorizedHandler): void {
  unauthorizedHandler = handler;
}

export class APIProblem extends Error {
  readonly status: number;
  readonly problem: Problem | undefined;

  constructor(status: number, problem?: Problem) {
    super(problem?.detail ?? `Request failed with status ${status}`);
    this.name = "APIProblem";
    this.status = status;
    this.problem = problem;
  }
}

export async function requestJSON<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.body !== undefined && !(init.body instanceof FormData)) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(path, { ...init, headers, credentials: "same-origin" });
  if (!response.ok) {
    let problem: Problem | undefined;
    if (response.headers.get("content-type")?.includes("application/problem+json")) {
      problem = (await response.json()) as Problem;
    }
    if (response.status === 401) unauthorizedHandler?.(path);
    throw new APIProblem(response.status, problem);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}
