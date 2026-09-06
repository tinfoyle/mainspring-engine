# Using the Spyglass workspace

Open `/app/workspace` for chat on its own. Choose Work, Knowledge or Documents to open a working view beside the same conversation. Close returns to chat alone. On a narrow screen, Chat and Back to view switch between the two without discarding the conversation.

## Everyday work

- **Work** holds tasks and responsibilities. Open an item to see its details or choose Use in chat.
- **Knowledge** holds saved information and its sources. Open a record to review, correct or explicitly add its current version to chat.
- **Documents** holds uploaded files. Upload a supported file of up to 50 MB, wait for checking and preparation, then publish the prepared revision if requested. Open the file to read its published passages; Use document in chat adds the entire published revision using the server’s existing frozen context mechanism. Search by title or expand Search inside published documents. The library and long files are paginated.
- **Needs you** opens the existing Your Turn review queue. Its count is refreshed from the server. Decisions and consequential proposed actions still require their existing approvals.
- **More** opens Schedules, Finance and Marketing. Pin frequently used views under Settings → Workspace preferences.

## Conversations

Chat stays mounted while working views change. History selects a team and a saved conversation or starts a new one; a new conversation can have an optional title. Agents chooses who replies and whether another agent summarizes the replies. Settings → Agents & tools configures agent teams, instructions and tool access.

Unsent drafts and the last conversation are saved in this browser tab, separately for each signed-in user and account. Reload restores them when browser storage is available. Closing the tab is not a durable draft backup; sent conversations are stored by the application. Exact unconfirmed sends reuse their request identity when retried after reload. Running conversations resume progress polling. Changing accounts clears the visible old-account context and opens the selected account’s workspace.

Opening a record does not silently attach it. Use in chat shows an explicit reference chip that can be removed before sending. The application checks source access again at send time and rejects changed Work or Knowledge versions. Document references must still belong to the current published revision. Whole-document context is subject to the existing 48 KiB run-context limit; oversized selections are rejected before a run starts. These references do not grant agents additional tools or permission to act.

The business interview remains available under Settings → Business profile. Its conversation persists while other views are open; More → Business interview returns to it. The business notebook appears in the working view. More → Agent conversations returns to the regular team chat. Interview answers retain their existing Knowledge and approved Work behavior.

## Settings and existing links

| Destination | Where to find it |
| --- | --- |
| Business interview and notebook | Settings → Business profile |
| Account team, invitations and ownership | Settings → People & access |
| Agent teams, versions and tools | Settings → Agents & tools |
| Integrations and connection management | Settings → Connected services |
| Billing and checkout | Settings → Billing & plan |
| Authentication and recovery | Settings → Security |
| Affiliate enrollment and statement | Settings → Referral program |
| Consent and privacy rights | Settings → Privacy preferences |
| Account exports | Settings → Export your data |
| Account closure | Settings → Close account |
| Sign out | Bottom of Settings |
| Staff analytics, traffic and support | Separate protected operations site |

Existing record, conversation, approval, security return, export and OAuth URLs remain supported. Restricted accounts retain billing, security, privacy and export access. Required owner security setup remains a focused screen. Settings and navigation visibility never substitute for server authorization.
