import type {
  AssignableMembershipRole,
  ChangeMembershipRoleRequest,
  ChangeMembershipStateRequest,
  CreateInvitationRequest,
  InvitationCreated,
  Membership,
  MembershipResult,
  Memberships,
  OwnershipTransfer,
  TransferOwnershipRequest
} from "./generated/api-types";
import { requestJSON } from "./client";

function base(accountID: string): string {
  return `/api/v1/accounts/${encodeURIComponent(accountID)}`;
}

function memberPath(accountID: string, membershipID: string): string {
  return `${base(accountID)}/memberships/${encodeURIComponent(membershipID)}`;
}

export function listMemberships(accountID: string): Promise<Memberships> {
  return requestJSON<Memberships>(`${base(accountID)}/memberships`);
}

export function currentMembership(accountID: string): Promise<Membership> {
  return requestJSON<MembershipResult>(`${base(accountID)}/membership`).then((value) => value.membership);
}

export function createInvitation(accountID: string, email: string, role: AssignableMembershipRole): Promise<InvitationCreated> {
  const input: CreateInvitationRequest = { email, role };
  return requestJSON<InvitationCreated>(`${base(accountID)}/invitations`, { method: "POST", body: JSON.stringify(input) });
}

export function changeMembershipRole(accountID: string, member: Membership, role: AssignableMembershipRole, reason: string): Promise<Membership> {
  const input: ChangeMembershipRoleRequest = { expected_version: member.version, role, reason };
  return requestJSON<MembershipResult>(memberPath(accountID, member.membership_id), { method: "PATCH", body: JSON.stringify(input) }).then((value) => value.membership);
}

function stateCommand(method: "POST" | "DELETE", path: string, member: Membership, reason: string): Promise<Membership> {
  const input: ChangeMembershipStateRequest = { expected_version: member.version, reason };
  return requestJSON<MembershipResult>(path, { method, body: JSON.stringify(input) }).then((value) => value.membership);
}

export function suspendMembership(accountID: string, member: Membership, reason: string): Promise<Membership> {
  return stateCommand("POST", `${memberPath(accountID, member.membership_id)}/suspensions`, member, reason);
}

export function reactivateMembership(accountID: string, member: Membership, reason: string): Promise<Membership> {
  return stateCommand("DELETE", `${memberPath(accountID, member.membership_id)}/suspensions`, member, reason);
}

export function removeMembership(accountID: string, member: Membership, reason: string): Promise<void> {
  const input: ChangeMembershipStateRequest = { expected_version: member.version, reason };
  return requestJSON<void>(memberPath(accountID, member.membership_id), { method: "DELETE", body: JSON.stringify(input) });
}

export function leaveAccount(accountID: string, member: Membership, reason: string): Promise<void> {
  const input: ChangeMembershipStateRequest = { expected_version: member.version, reason };
  return requestJSON<void>(`${base(accountID)}/membership`, { method: "DELETE", body: JSON.stringify(input) });
}

export function transferOwnership(accountID: string, actor: Membership, target: Membership, reason: string): Promise<OwnershipTransfer> {
  const input: TransferOwnershipRequest = {
    target_membership_id: target.membership_id,
    expected_actor_version: actor.version,
    expected_target_version: target.version,
    reason
  };
  return requestJSON<OwnershipTransfer>(`${base(accountID)}/ownership-transfers`, { method: "POST", body: JSON.stringify(input) });
}
