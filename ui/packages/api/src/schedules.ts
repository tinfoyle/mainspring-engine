import type {
  CreateScheduleRequest,
  ReviseScheduleRequest,
  Schedule,
  SchedulePage,
  ScheduleHistoryPage,
  ScheduleTrigger,
  TransitionScheduleRequest
} from "./generated/api-types";
import { requestJSON } from "./client";

export type ScheduleRevision = Omit<ReviseScheduleRequest, "expected_version">;

const pendingOperations = new Map<string, string>();

function base(accountID: string): string {
  return `/api/v1/accounts/${encodeURIComponent(accountID)}/schedules`;
}

function command<T>(method: "POST" | "PUT" | "DELETE", path: string, payload: unknown): Promise<T> {
  const body = JSON.stringify(payload);
  const fingerprint = `${method} ${path} ${body}`;
  let key = pendingOperations.get(fingerprint);
  if (!key) {
    key = crypto.randomUUID();
    pendingOperations.set(fingerprint, key);
  }
  const pendingKey = key;
  return requestJSON<T>(path, { method, headers: { "Idempotency-Key": pendingKey }, body })
    .then((result) => {
      pendingOperations.delete(fingerprint);
      return result;
    });
}

export function listSchedules(accountID: string, cursor?: string, limit = 30): Promise<SchedulePage> {
  const query = new URLSearchParams({ limit: String(limit) });
  if (cursor) query.set("cursor", cursor);
  return requestJSON<SchedulePage>(`${base(accountID)}?${query}`);
}

export function getSchedule(accountID: string, scheduleID: string): Promise<Schedule> {
  return requestJSON<Schedule>(`${base(accountID)}/${encodeURIComponent(scheduleID)}`);
}

export function createSchedule(accountID: string, input: CreateScheduleRequest): Promise<Schedule> {
  return command("POST", base(accountID), input);
}

export function reviseSchedule(accountID: string, schedule: Schedule, input: ScheduleRevision): Promise<Schedule> {
  return command("PUT", `${base(accountID)}/${encodeURIComponent(schedule.id)}`, { ...input, expected_version: schedule.version });
}

function transition(accountID: string, schedule: Schedule, operation: "pauses" | "resumptions", reason: string): Promise<Schedule> {
  const input: TransitionScheduleRequest = { expected_version: schedule.version, reason };
  return command("POST", `${base(accountID)}/${encodeURIComponent(schedule.id)}/${operation}`, input);
}

export function pauseSchedule(accountID: string, schedule: Schedule, reason: string): Promise<Schedule> {
  return transition(accountID, schedule, "pauses", reason);
}

export function resumeSchedule(accountID: string, schedule: Schedule, reason: string): Promise<Schedule> {
  return transition(accountID, schedule, "resumptions", reason);
}

export function deleteSchedule(accountID: string, schedule: Schedule, reason: string): Promise<Schedule> {
  const input: TransitionScheduleRequest = { expected_version: schedule.version, reason };
  return command("DELETE", `${base(accountID)}/${encodeURIComponent(schedule.id)}`, input);
}

export function triggerSchedule(accountID: string, schedule: Schedule, reason: string): Promise<ScheduleTrigger> {
  const input: TransitionScheduleRequest = { expected_version: schedule.version, reason };
  return command("POST", `${base(accountID)}/${encodeURIComponent(schedule.id)}/triggers`, input);
}

export function getScheduleHistory(accountID: string, scheduleID: string): Promise<ScheduleHistoryPage> {
 return requestJSON<ScheduleHistoryPage>(`${base(accountID)}/${encodeURIComponent(scheduleID)}/history`);
}
