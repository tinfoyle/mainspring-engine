# ADR-0024: Make the documented business baseline the onboarding spine

Status: Accepted for MVP  
Date: 2026-08-11

## Context

A small-business owner rarely has a complete, organized electronic record set. A form can collect profile fields, but it cannot distinguish a confirmed fact from an assumption, show why a record matters, or turn missing evidence into accountable work. Agents also need a stable, source-attributed representation of the business before they can safely recommend improvements.

## Decision

Mainspring's primary onboarding is a versioned business-baseline assessment owned by the application.

The assessment moves through an explicit state machine: interview, evidence inventory, gap review, plan approval, active execution, and baseline ready. Interview answers become editable business facts with source and confidence. A governed evidence catalog covers identity, compliance, finance, sales, operations, customer service, workforce, security, privacy, product delivery, and strategy; an explainable scope selector uses the confirmed industry, services, team size, and priority to choose only applicable catalog entries. The original standalone source-access phase remains a database compatibility value, but the application automatically skips it because it presents no meaningful onboarding decision.

Initial onboarding uses only uploaded documents and public-web research, exposed in context inside the evidence interview, so setup can reach value without a separate source-selection screen or third-party authorization. After the baseline plan is created, owners may optionally grant narrow read-only access to selected inbox folders and date ranges or selected Google Drive folders. Every imported or discovered item retains connector provenance; public research retains the exact query, result, retrieval time, and citation. A search result does not become evidence until it is linked to a requirement.

Missing evidence is never treated as the end of the workflow. Each gap receives a disposition (locate, search, create, obtain, or not applicable), a responsibility (agent, owner, shared, or external), and an optional renewal date. The owner must explicitly approve creation of one parent plan and its child tickets. Approval creates internal work only; it does not purchase, file, send, or authorize an external action.

Completing linked work confirms the associated requirement. Expired evidence becomes stale, renewal tickets are created ahead of due dates, and a versioned reassessment carries facts forward while preserving historical assessments.

The documented baseline continues after onboarding as a closed knowledge loop. Every agent invocation begins with an application-enforced search of the tenant document library. All agents may create internal knowledge documents and publish complete new revisions of existing documents; each revision retains the responsible persona, run, invocation, change summary, and prior content. When the final agent contribution does not deliberately create or update a better artifact, Mainspring publishes a fallback knowledge record containing the request, contribution, findings, recommendations, and unresolved questions. Conversation attachments guide relevance but do not silently remove access to the rest of the authorized company library.

## Consequences

- Agents receive a documented, attributable operating context instead of unchecked form values.
- The evidence interview is business-specific and exposes both its selected topics and the reason for the scope; software, field-service, professional-service, retail, and general profiles are regression-tested.
- The product can help owners who do not know where records live without silently broadening access.
- Missing documents become visible work, including documents the business must create or obtain.
- Useful conversations and completed ticket work become durable, searchable records for future agents instead of disappearing into chat history.
- Document writes are attributable and recoverable through retained revisions; agents do not replace prior content without history.
- Baseline readiness is measurable and repeatable; later premium analysis can build on it without being part of onboarding.
- Connector implementations must enforce scopes and preserve provenance, and the UI must expose search and ingestion traces.
- The legacy form flow remains temporarily available for compatibility tests, but it is not the primary product path.
