# Stage unified workspace verification — 2026-09-06

## Reviewed deployment

- Application images: `0.3.0-rc.61`, source `68a439794acc1efe1113b6910c2710a6214e693d`.
- Manifest commit: `535863d6d0fb7baeb91d1b9106d68dca3ac6c3e8`.
- Active configuration checkout: `296e467ffb19a08803adcb14650ece8048044353`. This adds the new routes to both Caddy configurations and the local HTTP smoke list; the RC61 image digests are unchanged.
- Global migration ledger: 72. Cell A/B ledgers: 90/90.
- Final Stage check: 53 long-running containers, 52 healthy, zero unhealthy. The internal edge has no container health check.
- All four earlier owner report schedules remain paused; no schedule was created during this verification.

Testing used only the owner's Infinite Ocean account. No other customer's records were changed. This verifies the workspace integration and its relevant regression paths, not every possible provider or business workflow.

## Live browser evidence

The owner uploaded a small synthetic text file through Documents. Scanning, extraction and publication completed; revision 1 was readable in the document view.

- Document: `85a5913e-ce57-8fff-b379-6e0c957f4487`, **Workspace verification — synthetic delivery details**.
- Final conversation: `7d92845c-954e-8172-9a71-dd40e1393b20`, **Workspace document verification — RC61**.
- Team: Weekly Operations (`eee01c43-a20a-49c5-b040-211fde575e3c`).
- Final run: `ef07231f-0844-41f4-a491-ecb8e1c835c8`, `succeeded`.
- Invocation: `3ed74353-3945-802c-a737-c0ef5fdbecf7`, `succeeded`; result queue `projected`, no error.

Use document in chat showed an explicit whole-revision reference. Reloading the document URL preserved the draft, conversation title and attachment. The read-only question asked for the delivery window and handling charge and explicitly prohibited tasks, schedules, email and record changes. After Send, the operator switched to Knowledge and then Settings, and reloaded Settings while the run progressed. The same conversation returned:

> Delivery window: Tuesday at 08:30. Handling charge: $17.40.

Its Sources section named the uploaded document. There was one user message and one agent reply. The completed conversation and fixture remain available for review.

Direct HTTP checks now return 200 for `/app/workspace`, `/app/settings`, `/app/documents` and the document detail URL. Browser reloads of the document and Settings routes also passed. The desktop Settings/chat layout was visually reviewed after completion.

## Defects found and repaired during the live check

**Document context and stuck failure.** RC60 put reference text in the prompt without registering the document in the existing immutable server context. Citation policy rejected the output correctly, but left the run appearing active. RC61 sends `context.knowledge_document_ids`, so the existing server admission freezes the authorized published revision. It does not weaken citation checks. Whole-document attachment is labelled explicitly and remains subject to the existing 48 KiB run-context limit.

Authenticated completed outputs rejected by output validation or immutable result policy now close as failed runs through the existing lease/account/digest-bound projection mechanism. Rejected output and actions are not published. Tampered envelopes remain quarantined. Cell migration 90 permits only the two application rejection codes for this completed-result failure path.

Only the exact owner RC60 test invocation `cd486fcf-5e6d-829d-ae9b-f985dd2280dc` was requeued from its policy-rejection dead letter after deployment. Run `bce3b552-60c2-44db-b7ab-f531193690e7` then showed a visible failure and recovery controls. The operator selected Accept failure with an explanatory test note; the recovery form cleared. The separate fresh RC61 run above succeeded.

**New route reloads.** The edge's explicit private-UI route list omitted Workspace, Settings and Documents. Navigation inside the already loaded app worked, but direct HTTP requests returned 404. Configuration commit `296e467` adds all four new paths, retains the existing API/auth routing, and extends local smoke coverage. Both Caddy configurations adapt successfully, and all 39 app route patterns match the private-UI route list.

## Automated checks

- Full Go suite and API contract check passed.
- Real PostgreSQL checks passed for account-scoped document preview, publication/deletion boundaries, immutable authorized context, atomic result projection, rejected completed-result failure, digest/code rejection and replay.
- Full UI unit suites passed during RC60 preparation: 116 customer, 11 operations, 22 public and 55 API cases. RC61's focused conversation suite passed all 10 cases, including whole-document registration.
- Full lint, app production build/type checking and asset budgets passed. Private JS is approximately 135 KiB gzip against a 140 KiB budget; CSS approximately 15 KiB against 20 KiB.
- 38 applicable legacy desktop browser cases passed, plus the phone keyboard/focus case.
- RC61 workspace browser suite: 32 passed across Chromium, Firefox, 360-pixel phone and 320-pixel reflow. Covers draft persistence, explicit references and document registration, run resumption, duplicate-send prevention, existing record links, business interview persistence, upload and toolbar preferences.
- Desktop and phone screenshots were reviewed. Both release publications passed the existing image scan, SBOM and provenance gates.

Local evidence logs are under `/home/rojo/.cache/spyglass-stage-audit/logs/`, notably `workspace-rc61-browser.log`, `workspace-rc61-go.log`, `workspace-rc61-pg.log`, `workspace-rc61-context-pg.log`, `workspace-rc61-unit.log`, and `deploy-rc61-edge.log`.

## Product boundaries

One optional working view sits beside persistent chat on desktop. On narrow screens the user switches between Chat and the working view; the conversation stays mounted. Opening a record does not silently send it to agents. Draft restoration is browser-tab storage, not a durable cross-device draft service. Server-side permissions and existing approvals remain authoritative.

Every previous route remains reachable. Configuration pages move into Settings, while Schedules, Finance and Marketing remain optional working views under More. The separate protected staff console stays separate. See [the user guide](workspace-user-guide.md) for the full destination map.
