# Monkeys Admin Panel: Features and Platform Control Plan

Date: 14 October 2026

Product: Monkeys

Owner: Buddhicintaka (OPC) Pvt. Ltd.

Admin app: `local/monkeys-admin`

Status: Proposed feature plan for review. This document does not approve or implement any money transfer or product change.

Quick navigation: [existing admin](#2-what-the-current-admin-app-already-has), [important gaps](#3-important-gaps-discovered-in-the-code), [payments and host releases](#4-first-priority-paid-event-money-and-host-payouts), [staff access](#5-access-only-authorised-people-see-the-admin-side), [all platform modules](#6-detailed-feature-catalogue-for-the-rest-of-the-platform), [delivery order](#10-delivery-order), [decisions to confirm](#12-business-decisions-to-confirm-before-implementation).

## 1. What we are planning

Monkeys needs a staff panel that lets authorised people operate the whole content and community platform. The first priority is paid events: know how much money came in, how much belongs to each host, what must be held, and when we approve a release.

The panel should answer five questions without asking an engineer to inspect the database:

1. What is happening on the platform?
2. What needs staff attention now?
3. What money do we owe, to whom, and why?
4. What can this particular staff member see or change?
5. Who made a decision, what changed, and can we prove it?

The user has asked for manual control over host releases. The recommended design is an event-by-event payout approval process. Staff discretion must mean a recorded decision with a reason, an amount, and an authorised approver.

The panel covers existing posts, topics, profiles, groups, events, discussions, notifications, account verification, storage, search, and creator tools. Podcast and live-stream administration are future modules and should appear only when those products are available.

### How to read feature status

- **Existing admin screen:** the local staff app already has a screen and calls a staff API.
- **Existing backend:** an API, database record, or service behaviour exists, but the staff screen may be missing or limited.
- **Existing product:** ordinary users already have the feature; staff administration still needs work.
- **Proposed:** a new capability recommended in this plan.
- **Future:** tied to a product that is not yet established by the reviewed code.

“Existing” means found in the checked-out source code. It does not mean that the feature was tested against production, that all migrations are deployed, or that the current provider account supports every proposed integration.

## 2. What the current admin app already has

### 2.1 Login and restricted access

**Existing admin screen and backend.**

- Staff log in using the normal account login and session cookie.
- The app recognises Admin, Support, Community, and Viewer roles.
- Viewer accounts are blocked from the staff interface.
- The gateway checks login and permitted roles for the staff routes.
- Staff routes also have a local/private-network restriction. Remote access will need an intentional network design, such as an approved private access gateway.
- The normal browser app does not send a shared admin secret.

The browser currently guesses the staff role from other API responses. A dedicated staff session response should replace this guessing and return explicit permissions.

### 2.2 Overview

**Existing admin screen and backend.**

- Total users and new users in the last seven days.
- Counts of published, draft, scheduled, and archived posts.
- Counts of draft, published, live, completed, and cancelled events.
- Counts of published, draft, archived, and suspended groups.
- Admin-only payment totals, including captured money, platform fee, tax amount, and open host payable.

These are basic counters. Detailed trends, attendance quality, discussion statistics, reconciled cash balances, and work queues are proposed additions.

### 2.3 Users

**Existing admin screen and backend.**

- Search by username or email and page through results.
- See account status, platform role, and verification state.
- Change a user's platform role as Admin.
- Suspend a user as Admin.
- Delete an account using the existing account deletion pipeline.
- The backend protects the last Admin from being demoted.
- Backend flag and unflag actions exist for suspicious accounts, although the current Users screen does not expose the full workflow.

Suspension currently records a status. The reviewed login and token-validation code does not check that suspended status, so it is not yet a dependable enforced ban.

### 2.4 Posts

**Existing admin screen and backend.**

- List posts by identifier, author, and status.
- Unpublish a post.
- Permanently delete a post through the existing deletion pipeline.
- An Admin-only API can identify search-index post IDs that are missing from the main post records.

The screen currently says “Blogs”. Use “Posts” in the new panel, while keeping existing backend identifiers compatible.

### 2.5 Events

**Existing admin screen and backend.**

- Search and list events, including drafts.
- See host, start date, and status.
- Cancel, unpublish, or delete an event.
- Paid-event restrictions prevent certain destructive changes.

The paid-event cancellation gap is described in Section 3. Cancelling through the current admin path should not be assumed to complete refunds.

### 2.6 Groups

**Existing admin screen and backend.**

- Search groups and see owner, member count, and status.
- Suspend a group.
- Permanently delete a group, subject to financial restrictions.

### 2.7 Account verification

**Existing admin screen and backend.**

- Separate pending/review, approved, and rejected requests.
- See account details and the submitted verification type.
- Review uploaded selfie and identity-document images through protected, short-lived links.
- Approve or reject a request, with a rejection reason.
- Record the reviewing staff account.

This is account verification. It must not automatically be treated as payment-provider approval or approval of the host's payout bank account.

### 2.8 Payments

**Existing admin screen and backend.**

- List events that have payment ledger rows.
- View attendee payments and host settlement records for an event.
- Record captured gross, platform fee, tax amount, and the calculated host share.
- Create a pending settlement for the entire currently open amount.
- Mark a pending settlement as paid, with a note.
- Store money in integer paise in the ledger.
- Record settlement creation and paid actions in the admin audit log.
- Reserve pending settlement amounts in the calculation so another settlement does not reserve the same open amount again.

The current workflow is bookkeeping for a bank transfer made elsewhere. It does not send a host payout through Razorpay. It has no separate approval step, partial-amount selection, verified bank details, or bank reconciliation screen.

The public frontend has paid-ticket and checkout code, but the new-event form currently hides paid-ticket setup and submits any newly created ticket tier as free. The management screen still supports priced tiers. Decide how paid-event creation is enabled as part of the finance release work.

### 2.9 Moderation

**Existing admin screen and backend.**

- Record an NSFW flag and a reason against a user, post, event, or group.
- Hide an event comment or question by entering its identifiers.
- Record moderation actions in the audit log.

The current “hide” implementation deletes the event comment/question row. A recoverable hide with retained evidence is a proposed change. A stored flag also does not, by itself, prove that the public site hides or labels the content everywhere.

## 3. Important gaps discovered in the code

These items should be handled before relying on the panel for full financial and platform control.

1. **Community staff can currently create settlements and mark them paid.** The registered gateway routes allow Admin and Community. Some older documentation says these actions are Admin-only. Use the live route definitions as the current source of truth, then introduce dedicated finance permissions.
2. **Admin cancellation does not run the host cancellation refund process.** It cancels the event and RSVP rows, but the reviewed service method does not invoke `refundAll` or attendee cancellation notifications. The host cancellation path does. Both paths need one reliable cancellation workflow.
3. **Pending reservations and completed payouts are added together under “settled”.** An amount set aside for a future transfer is not the same as money already paid to a host. Show both separately.
4. **A settlement can be created without checking whether the event has finished or whether a financial hold exists.** The present code checks open payable, not the proposed release conditions.
5. **Refund completion is assumed too early.** The current refund client returns an ID; the service then marks the payment refunded without tracking the provider's final refund status. Razorpay distinguishes refund creation, processing, and failure. See the provider references in Section 15.
6. **Refunds and captures are not a complete reconciliation process.** The webhook parser handles captured and failed payments, but the finance workflow needs refund, dispute, provider settlement, and bank-transfer outcomes too.
7. **A refund after payout can hide a financial shortfall.** `OpenPayable` clamps negative results to zero. The panel needs a visible amount owed back by the host or a recorded platform loss, rather than hiding the deficit.
8. **The payment search box promises host search, but the reviewed SQL searches event title and slug.** Add an actual host filter and test it.
9. **Most detail lists are too limited for large events.** The payment detail API returns all payment and settlement lines together. Add server pagination and separate transaction filters.
10. **Staff roles are inferred from statistics and list responses.** This can become wrong when permissions change or an optional API fails. Return explicit staff permissions from the server and deny access when they cannot be established.
11. **The phone navigation is incomplete.** The stylesheet hides the side navigation below 900px; the reviewed app has no replacement mobile navigation. Staff need a reachable menu and logout on smaller screens.
12. **Some legacy admin routes are placeholders.** Suspicious-user detection, flagged-user retrieval, and system statistics in the key-gated router do not provide the complete functionality their names suggest. Its health response contains a fixed timestamp. Do not show these as working monitoring tools.
13. **Sensitive actions are wider than the eventual staff roles should allow.** Community staff currently have catalogue takedown and deletion access, payment access, and verification access. Split these responsibilities explicitly.
14. **The admin app directory is ignored by the backend repository.** `local/*` is ignored and the checked folder does not have its own repository. Before implementing the new panel, agree where its code will be versioned and reviewed.
15. **A legacy verification review path checks login but not staff permission.** `/api/v1/auth/verification/review` and its reviewed service handler do not enforce a reviewer role. The protected new admin screen is not sufficient while another write path bypasses that permission. Restrict or retire the legacy route.
16. **Account and group suspension need enforcement.** Suspended accounts are not refused by the reviewed login/refresh/token-validation methods. The reviewed group publishing method also does not prevent an editor from publishing a suspended group again. Restriction must be enforced across user actions and status transitions.
17. **Published content is not always public content.** The reviewed post-media permission check makes published assets public without checking the members-only audience. Event/group media reads also need visibility review. Apply resource audience rules to direct file access and review cache invalidation after a takedown.
18. **A successful delete response is not proof that every service finished cleanup.** Queue-driven work can still be outstanding in search, storage, and notifications. Staff need a deletion job/status view with visible failures.
19. **Some product controls are present but incomplete.** Post comment buttons, optional communication switches, newsletter subscriptions, and native live streams should not be counted as fully delivered features just because a visible button or schema field exists.

These are source findings, not a report of a reproduced production incident. This task changes documentation only.

## 4. First priority: paid-event money and host payouts

### 4.1 Use clear names for the three money movements

1. **Ticket collection:** the attendee pays for an event.
2. **Provider settlement:** Razorpay sends collected money, after its own deductions, to the platform's bank account.
3. **Host payout:** the platform releases an approved amount to the event host.

They are separate records. A captured ticket does not prove that money has arrived in our bank account, and a provider settlement does not prove that we paid the host. Razorpay describes settlement as transferring collected funds to the merchant's bank account after deductions. See [Razorpay settlement documentation](https://razorpay.com/docs/payments/settlements/).

Do not label host money as an “escrow account” unless the actual provider and banking arrangement has been approved for that purpose. The panel records money owed to hosts and money held under the published payout policy.

### 4.2 Finance overview

**Proposed.** A finance staff member should see:

- Total ticket money captured in the selected period.
- Total refunds completed and refunds still processing.
- Platform fees, recorded tax amounts, and payment-provider charges as separate values.
- Host share earned, host payouts reserved, and host payouts completed.
- Money held because an event has not finished, a complaint is open, or a bank account is unverified.
- Money expected from the provider and money actually matched to bank deposits.
- Approved transfers waiting for execution.
- Transfers with an unknown result, failed transfers, and reversed transfers.
- Hosts with a negative balance or recovery case.
- Unmatched payments, missing callbacks, and overdue refund jobs.

Every total needs a date range, currency, definition, last-updated time, and a link to the transactions behind it. Missing data must show “unavailable”, not zero.

### 4.3 Paid-events list

**Extend the existing Payments screen.** Each row should show:

- Stable event ID, title, public link, group, and host.
- Event date, timezone, status, and series occurrence when relevant.
- Ticket types, paid attendee count, successful payments, and failed/pending attempts.
- Lifetime gross captured and current money remaining after refunds, clearly separated.
- Host earnings, holds, payout reservations, successful payouts, and remaining amount.
- Latest refund or dispute warning.
- Release status and next required action.

Filters: host, event, group, date range, event status, payment state, payout state, hold reason, currency, amount range, and unresolved problems. Use a stable event ID for financial links even if a title, username, or slug changes.

### 4.4 Event finance detail

**Proposed expansion.** Use separate tabs or sections for:

1. Event and host summary.
2. Ticket orders and payment attempts.
3. Successful collections.
4. Refunds and disputes.
5. Host share and deductions.
6. Holds and release decisions.
7. Payouts and transfer proof.
8. Provider and bank matching.
9. Ledger and staff history.

The top summary must separate “reserved for a payout” from “paid to the host”. The primary action should describe the exact operation, for example “Prepare payout request” or “Record completed bank transfer”.

### 4.5 Ticket payment record

**Existing records, with new detail fields and workflow.** Store or expose:

- Order ID, payment ID, event ID, attendee reference, and ticket type.
- Expected price and currency at the time of purchase.
- Provider-confirmed amount and currency, not just the application's expected amount.
- Payment attempt state and timestamps.
- Capture, refund, and dispute references.
- The fee rules that applied when the payment was captured.
- A link to the RSVP and notification history.
- A reconciliation state and any mismatch reason.

Show attempted payments even if they never produced a captured ledger row. Do not show complete card details or payment credentials.

### 4.6 Host payment profile

**Proposed.** Give the host a finance profile with:

- Stable account ID, public username, and legal payee name.
- Verified payout destination, masked account details, and verification date.
- Payment-provider onboarding status, if that provider requires it.
- Country and supported payout currency.
- Required business/tax details, only when applicable to the approved business arrangement.
- All events, earnings, holds, refunds, payouts, and balance statements.
- Bank detail change history and internal finance notes.

Changing a payout destination should require verification and invalidate any outstanding approval tied to the old destination. A public verification badge is not proof that a bank destination is safe to pay.

### 4.7 Release workflow controlled by our team

**Proposed.** Recommended starting workflow:

1. Captured tickets add to the event's host earnings using the recorded fee split.
2. The system identifies the money that can be considered for release. Event completion, unresolved refunds, disputes, holds, provider settlement, and destination verification affect this amount.
3. A permitted finance staff member prepares a request for a specific amount and event. Partial releases may be allowed, but cannot exceed the available amount.
4. The request records its reason, destination, balance calculation, and supporting documents.
5. A different permitted person approves or rejects it. The same person should not prepare and approve a payout.
6. An approved request reserves the amount so another request cannot use it.
7. Staff execute the bank transfer outside the panel in the first version, or a future approved provider integration executes it.
8. Staff record the transfer reference, date, actual amount, payee, and evidence.
9. The transfer is matched to bank/provider evidence. Only then is it treated as completed.
10. The host receives a statement describing the amount, deductions, and transfer reference.

Approval does not override an insufficient balance, a duplicate transfer, an unverified destination, or a refund/dispute hold. If a balance or destination changes after approval, the request must be reviewed again before execution.

Approval and balance reservation must happen together in one database transaction. Captures, refunds, holds, and other payout decisions must use a consistent balance check so concurrent activity cannot approve more than is owed.

Recommended payout states: prepared, awaiting approval, rejected, approved, on hold, executing, awaiting confirmation, completed, failed, cancelled before transfer, and reversed. Show the reason and history behind each transition. The current pending/paid fields are insufficient for this full workflow.

Manual bank transfers happen outside the software. The panel can check reservations and repeated references, but it cannot prevent someone sending a duplicate transfer directly from the bank. Use the bank's approval controls and daily matching as well as the panel's controls.

For a small team without two finance staff, an owner may approve an explicitly documented exception. It must remain visible in the audit log; it must not be the default hidden path.

### 4.8 Holds, failed payments, and reversals

**Proposed.**

- Hold an event's host release or an entire host's releases, with a reason, amount, reviewer, and review date.
- Distinguish a hold from a permanent deduction. A hold delays release; it does not reduce what the host earned.
- Record partial holds and release them in a controlled way.
- Cancel a payout request before execution and release its reservation.
- Keep failed attempts in history. Release a reservation only when we know that no money moved.
- If a bank transfer result is unknown, investigate and match evidence before sending it again.
- Record reversals as new financial movements linked to the original transfer.
- Show overpayments and host debts explicitly. Do not hide a negative balance by displaying zero.
- Allocate any recovery from a later event transparently and only under the agreed host policy.

### 4.9 Refunds and disputes

**Existing automatic refund calls in the host cancellation flow; proposed staff case management and reliable completion tracking.**

- Show every requested refund, its event, attendee, amount, reason, and provider reference.
- Support refund approval and rejection according to the published event policy.
- Support full refunds first; add partial refunds only with a clearly defined split and accounting rule.
- Separate “requested”, “approved”, “submitted to provider”, “processing”, “completed”, and “failed”.
- A provider-created refund is not the same as a completed refund. Track final processing/failure messages or check provider status. [Razorpay refund webhook documentation](https://razorpay.com/docs/payments/refunds/subscribe-to-webhooks/) lists distinct creation, processing, and failure events.
- Put refund work in a durable job queue so a service restart does not lose it.
- Retry failed work with duplicate protection. Check the provider before retrying an outcome that is uncertain.
- Reserve the relevant refund exposure before releasing host money.
- Freeze or revalidate pending payout requests when a refund or dispute changes the available balance.
- Support refunds after host payout by recording the resulting debt or loss and the agreed recovery action.
- Keep complaint evidence, deadlines, staff decisions, and provider responses for payment disputes.
- Notify attendees only when the displayed refund status is true.

Cancelling a paid event from either the host side or the staff side must create the same refund and attendee-notification work. Cancelling the event must not mark its refunds completed immediately.

### 4.10 The permanent ledger

**A basic capture ledger exists. A complete accounting ledger is proposed.**

A ledger is the permanent history of money coming in, money going out, and money we still owe. The new ledger must add entries instead of erasing or rewriting financial history.

Record ticket collections, host earnings, fees, tax amounts, provider charges, refunds, host payouts, reversals, and approved corrections. Holds and payout reservations should also have a history, but must not look like bank transfers.

Each financial entry should have:

- Unique ledger entry and transaction-group IDs.
- Event ID, host ID, and attendee/order reference where applicable.
- Amount in integer paise and currency.
- Type of movement and its effective date.
- Payment, refund, settlement, or bank transfer reference.
- Original entry reference for a correction or reversal.
- Provider event ID or request ID used to prevent duplicates.
- Staff actor or service actor, action time, reason, and approval reference.
- Accounting period and reconciliation state.

Use balanced accounting entries: each movement records where money came from and where it went. For example, a confirmed ticket capture creates money receivable from the provider and the appropriate host, platform, and tax obligations. An actual provider bank deposit moves the receivable into bank cash. A host payout reduces both bank cash and the amount owed to that host.

Keep this accounting history separate from the editable screen status. A mistaken record is corrected by a linked reversing/correcting entry. Closed accounting periods cannot be silently changed.

The database, server permissions, and audit storage must enforce these rules. Merely disabling an Edit button does not make records permanent.

### 4.11 Calculations in plain English

The current code records a 5% platform fee and 18% GST on that fee. Its example is:

- Attendee payment: ₹1,000.
- Platform fee: ₹50.
- Recorded GST on the platform fee: ₹9.
- Recorded host share: ₹941.

This describes the current software rule, not a conclusion about the company's correct tax treatment. Confirm the fee, tax, provider-charge, invoice, and refund rules with the responsible finance adviser before implementing a new policy.

For ten successful ₹1,000 tickets under that existing rule, the captured total is ₹10,000 and the calculated host share is ₹9,410. This is not proof that ₹10,000 has reached the platform bank account.

Recommended event balance calculation:

1. Start with the host shares recorded for successful captures.
2. Apply completed host-attributable refund/reversal adjustments and approved corrections.
3. Subtract successful host payouts.
4. Subtract amounts reserved for outstanding approved/executing payouts.
5. Subtract active release holds.
6. Show the remaining amount and any negative balance separately.

A pending refund must either reduce the available balance through a reservation or through the appropriate accounting adjustment, never both. The refund policy must specify what happens to platform fees, tax amounts, and provider charges.

An eligible host balance is only one limit. The release must also fit within verified available cash after the platform's other earmarked obligations. Do not spend money that belongs to a different event or host.

Fee policies should have versions and effective dates. A policy change must not silently recalculate old ticket payments. A partial payout and a later refund must preserve the original purchase split.

### 4.12 Reconciliation and reports

**Proposed.** Reconciliation means matching our records with the provider and bank records.

- Match captured payments to provider payment records.
- Match provider settlement batches to bank deposits and explain provider deductions.
- Match completed refunds to provider refund results.
- Match each host payout to a bank transfer reference and destination.
- Find missing, repeated, mismatched, or unconfirmed transactions.
- Open an investigation case instead of silently adjusting a total.
- Keep a daily reconciliation result, responsible reviewer, and unresolved items.
- Import statements through a validated process with a preview, duplicate checks, and a recorded source.
- Offer event, host, date, fee, refund, hold, payout, and reconciliation reports.
- Produce host statements, payment receipts, fee invoices, and correction documents only under the approved accounting rules.
- Allow finance exports with permission checks, masked personal details where possible, and an audit entry.

Current backend payment responses need extra fields for dates, refund details, separate reservation/paid totals, transfer references, and approval history. These cannot be supplied reliably by a screen redesign alone.

## 5. Access: only authorised people see the admin side

### 5.1 Recommended staff responsibilities

**Proposed permissions; these are not the current four platform roles.**

- **Platform owner:** manages staff access, approves major policy changes, and handles exceptional decisions.
- **Finance operator:** reviews payments, prepares payout requests, records transfers, and investigates mismatches.
- **Finance approver:** approves or rejects releases, refunds above the agreed threshold, and financial adjustments.
- **Moderator:** reviews reports and controls harmful content, with no payout or bank access.
- **Community manager:** manages approved group/event support tasks and community quality, with no financial release rights by default.
- **Support agent:** handles cases and sees the minimum user/order information needed to help.
- **Verification reviewer:** reviews protected verification evidence and decisions.
- **Operations engineer:** sees service health and runs approved recovery operations.
- **Read-only auditor:** reads approved records and history without changing them.

A staff member may have more than one responsibility, but each permission must be explicit. A group owner, event host, co-host, or verified creator is not automatically platform staff.

### 5.2 Access controls to build

- Return explicit staff roles and permissions from a dedicated staff-session endpoint.
- Enforce every permission on the server, including direct API calls and exports.
- Deny access by default when a role is missing, revoked, or unknown.
- Support staff invitations, activation, suspension, and immediate session revocation.
- Require stronger sign-in and recent confirmation for payouts, role changes, and sensitive exports.
- Apply private-network/proxy rules deliberately and test trusted proxy behaviour.
- Protect state-changing cookie-authenticated requests against cross-site request forgery.
- Give staff access an expiry where appropriate and review access regularly.
- Prevent removing the final owner and prevent staff granting permissions they cannot manage.
- Log access to identity documents, private content, bank details, and exports.
- Hide private bank and identity details from unrelated roles, including in API responses.
- Keep infrastructure secrets and shared admin keys out of browser bundles and screenshots.
- Test that a revoked staff account loses access even when it has an old session token.
- Show a useful access-denied screen and retain a visible logout path.

The current Community role combines several responsibilities. Migrate its permissions explicitly rather than assuming every Community account should keep all current powers.

## 6. Detailed feature catalogue for the rest of the platform

The following sections describe the admin controls the platform should have. Each starts by identifying the existing foundation.

### 6.1 Staff home and work queues

**Existing:** basic overview counters.

**Add:**

- Work waiting for me: reports, verification reviews, support cases, refunds, and payouts.
- Urgent risks: cancelled paid events without completed refunds, overdue transfers, failed jobs, and serious abuse reports.
- User and creator growth with selectable date ranges.
- Event participation, group activity, discussion activity, and content publication trends.
- Recent staff decisions and service incidents.
- A search that finds accounts, events, groups, posts, discussions, payments, and cases by stable ID.
- Saved views, task ownership, due dates, reminders, and links to related records.

### 6.2 Accounts and profiles

**Existing:** account search, roles, suspension, deletion, flags, profiles, follows, and account settings.

**Add:**

- A complete user detail view linking posts, discussions, groups, events, verification, reports, and support history.
- Separate account status, email verification, public badge, staff permissions, and payout verification.
- Enforce account restrictions in login, token validation, refresh, and every relevant write path; expire existing access when a ban takes effect.
- Warning, temporary restriction, suspension, ban, reinstatement, and appeal workflows.
- Restrictions by action: posting, replying, creating groups, hosting events, or accepting paid bookings.
- Fraud/spam signals and staff notes, with human review before punitive action.
- Safe email/username-change support and account recovery; staff must never read a user's password.
- Access/session revocation and suspicious-login investigation.
- Connected deletion-impact preview, including paid-event and ledger restrictions.
- Duplicate-account investigation and approved recovery/ownership-transfer cases.
- User data export and deletion requests with tracked scope, status, retention rules, and completion proof.

### 6.3 Verification and identity review

**Existing:** submitted verification evidence, protected image viewing, approval, and rejection.

**Add:**

- Assign a request to a reviewer, claim it, and prevent conflicting decisions.
- Request more information, return a request for correction, and record an expiry/review date when relevant.
- Add search and queue navigation that uses the backend's existing pagination instead of only the current limited first-page results.
- Decision categories and consistent rejection explanations.
- Document-viewing audit, short link lifetimes, and restricted export permissions.
- Retention and deletion schedules for identity documents.
- A correction/revocation workflow for mistaken public verification.
- Separate financial onboarding evidence from public identity/badge verification.
- Do not display identity evidence on public profiles, feeds, SEO pages, or general staff searches.

### 6.4 Posts, authors, and the editor

**Existing:** rich posts, drafts, publication/scheduling, authorship, access scope, topic discovery, group-scoped posts, and admin unpublish/delete actions.

**Add:**

- Search by title, author, topic, publication state, group, scope, date, and report status.
- Open a safe preview with visibility and authorship information.
- See version history, scheduling failures, attached media, and publication errors.
- Review reported posts, issue warnings, unpublish, restore, or delete under the appropriate permission.
- Show why an unpublished/deleted post will disappear from feeds, search, sitemaps, and RSS.
- Distinguish private drafts and private-group posts from public content; allow private preview only for a permitted case.
- Review co-author/permission disputes and changes to publication scope.
- Review author claims and plagiarism/copyright reports with evidence and appeals.
- Curate featured posts and authors with reasons, time limits, and placement previews.
- Find broken post records, search-index mismatches, and missing media through a repair queue.
- Reproduce editor issues on test content; do not rewrite an author's work silently.

### 6.5 Discussions and replies

**Existing product:** discussions, threaded replies, mentions, links/previews, likes, and group audience rules. The current admin app has no Discussions screen.

**Add:**

- Browse public and permitted private-group discussions separately.
- Search by author, group, date, text, mention, and moderation status.
- View reply threads with the surrounding conversation needed to judge a report.
- Hide/restore a discussion or reply, lock a thread, and restrict abusive participation.
- Trace mention spam, repeated links, impersonation, and coordinated abuse.
- Inspect suspicious link previews without making the staff browser automatically load unsafe remote content.
- Retain moderation evidence, a reason, reviewer, appeal state, and affected visibility.
- Remove content from discovery and search consistently after moderation.

### 6.6 Events and attendance

**Existing product:** event publishing, online/in-person events, ticket types, RSVP, capacity/waitlists, co-host permissions, attendee management, host review, recurring events, comments, photos, and payment flow. Question records and staff question-removal actions also exist in the backend; this does not establish a complete question editor in the public frontend.

The management UI also supports percentage-discount coupons, attendee CSV exports, and check-in updates. Some settings exist only in types or read views, such as registration-question fields and FAQ display; verify their create/edit workflow before promising staff control over them.

**Add:**

- A full event detail view linking host, co-hosts, group, occurrence, tickets, attendees, reports, and finances.
- Filters for cancelled events, missing information, suspicious hosts, sold-out events, and events with outstanding refunds.
- Approved staff correction of event details with before/after history and attendee communication.
- Staff-assisted host/co-host disputes and controlled ownership transfer.
- Review attendance, waitlist, host-screening requests, ticket limits, and duplicate bookings.
- Inspect coupons, discount limits, uses, expiry, eligibility, and their effect on the actual captured ticket price.
- Diagnose check-in corrections, unused tickets, and no-show data without inventing attendance that was not recorded.
- Review capacity changes against existing reservations and successful payments.
- Cancel one series occurrence or a whole series with a clear preview of affected bookings and money.
- Verify that private online meeting links are visible only to eligible confirmed attendees and permitted staff.
- Manage attendee complaints, safety incidents, misleading descriptions, and no-show/non-delivery cases.
- Show cancellation and refund progress together, including failed attendee notifications.
- Offer attendance export only to authorised roles and record the export.
- Curate featured events without treating “live” event status as proof that a video stream exists.

### 6.7 Groups and community governance

**Existing product:** group ownership, membership, roles, join rules/requests, invitations, group events, group posts, and group discussions. Admin can list, suspend, and delete groups.

**Add:**

- Full group detail: privacy, owner, rules, moderators, members, related content/events, and history.
- Membership abuse cases, disputed bans, owner succession, and abandoned-group recovery.
- Revoke abusive invitations and inspect repeated invitation failures.
- Suspend/restore a group with a reason and an impact preview.
- Prevent owners or editors from undoing a staff suspension through ordinary publish/edit routes.
- Review group-linked content consistently when group privacy or membership changes.
- Preserve event finance references when a group is suspended or removed.
- Support curated group discovery with expiry and placement controls.
- Keep private-group content out of public searches, recommendations, feeds, and exports.

### 6.8 Reports, moderation, and appeals

**Existing:** limited flags, event report records, comment/question removal actions, and a public post-report form that submits categories and notes. The post form calls `/reports`, but a matching registered gateway route was not found in this review, so end-to-end post report handling needs verification. A complete staff reports inbox is proposed.

**Add:**

- One reports inbox for users, posts, discussions, replies, events, groups, comments, questions, photos, and creator artifacts.
- Report category, severity, evidence, reporter, affected person, and linked cases.
- Deduplicate repeated reports without losing each report's origin.
- Assign reviewer, due date, internal notes, and escalation path.
- Actions: no violation, warning, restricted distribution, age-sensitive label, recoverable hide, removal, account restriction, and appeal review.
- Keep policy version, decision reason, affected content snapshot, and notifications.
- Reverse a mistaken action and record the reversal.
- Track repeated offenders and abuse of the reporting system.
- Verify that an NSFW decision is honoured in public rendering, discovery, thumbnails, and direct URLs.
- Limit access to dangerous or sensitive evidence and avoid automatically displaying it in list views.

### 6.9 Support and communications

**Existing product/backend:** contact submission and transactional notifications. A complete staff case desk is proposed.

**Add:**

- Cases for login, account recovery, verification, content, event attendance, refunds, and payouts.
- A timeline of messages, decisions, and linked records.
- Templates for payout holds, refund progress, moderation decisions, and event cancellation.
- Staff assignment, priority, response targets, escalation, and satisfaction tracking.
- Role-limited views of the information needed for a case.
- A clear separation between an internal note and a message the user will receive.
- No invisible impersonation. If a future “view as user” tool is added, prefer read-only simulation with explicit approval and audit.

### 6.10 Notifications and email delivery

**Existing product/backend:** in-app notifications; event and community actions produce notifications. Optional notification preference controls are incomplete.

The frontend notification flow uses an external notification integration as well as legacy service paths. Several optional email/WhatsApp preference controls are disabled. Legacy notification creation and some RPCs are incomplete, so delivery and preference support must be verified per path.

**Add:**

- Search delivery by user, event, action, and date.
- See attempted, queued, delivered, failed, and retried messages where the delivery provider supports those facts.
- Retry a failed transactional message with duplicate protection.
- Review templates, preview variables, and test messages using test recipients.
- Control announcements by audience, approval, send schedule, and cancellation.
- Preserve user opt-outs for optional/marketing messages and keep required service notices separate.
- Link payout/refund messages to the real financial state.

### 6.11 Media, files, and creator tools

**Existing product/backend:** post/profile/event/group media, storage services, verification assets, social snapshots, and digital business cards.

**Add:**

- A file inventory linking each asset to its owner, resource, visibility, size, and upload date.
- Broken-file and unused-file queues, with previewed cleanup and reference checks.
- Quarantine suspicious uploads and review malware/unsafe-media scanning results if a scanner is introduced.
- Diagnose missing images, rejected file types, failed conversions, and delivery errors.
- Apply per-account upload limits and investigate unusual storage usage.
- Review public snapshots/cards and content complaints using the same report workflow.
- Separate temporary export/render jobs from saved public/private artifacts.
- Never clean up a file still needed by a live resource, a permitted draft, an editor recovery window, or retained evidence.
- Keep verification documents in their private storage path with separate permissions.

### 6.12 Topics, search, recommendations, and homepage curation

**Existing product/backend:** topic discovery/following, search, feeds, and featured content components. Recommendation-related components and handler code exist, with backend availability requiring verification.

Some featured-author pins, announcements, and homepage selections are controlled in source code. Recommendation components or handler code are not proof that a recommendation endpoint is registered and operating. The new panel should replace the specific code-controlled placements that the team actually uses.

**Add:**

- Create/review/rename topics, merge duplicates, and retain old URL redirects.
- Map topic aliases without creating uncontrolled empty search pages.
- Review search results for missing, stale, private, or removed content.
- Repair or rebuild an affected resource's search entry through an approved job.
- See index lag, failed indexing jobs, and source/index counts.
- Curate homepage posts, hosts, authors, groups, and events with a preview, schedule, and expiry.
- Record manual ranking/promotional rules and their reasons.
- Track recommendation complaints, repeated exposure, and inappropriate audience matching.
- Never use staff promotion to expose private resources or override financial/moderation holds.

### 6.13 SEO and public discovery

**Existing product:** metadata, canonical URLs, structured data, robots rules, sitemaps, RSS feeds, and AI discovery text.

**Add:**

- Check missing titles/descriptions/images, bad canonical URLs, and invalid public links.
- Monitor sitemap/feed generation failures and stale public resources.
- Show whether a resource is public/indexable and why.
- Validate that private groups, drafts, invite tokens, bank data, and meeting links never enter discovery output.
- Manage redirects after approved slug/topic changes.
- Review preview images and publisher identity consistency.
- Import search performance/crawl reports only through approved connections.
- Preview and approve controlled SEO settings. Do not provide unrestricted robots editing to every staff role.
- Keep the admin app out of search indexes and expose no private data through public build assets. Login and server permissions provide access control; noindex alone does not.

### 6.14 Product analytics

**Existing:** basic staff counts and activity data. Detailed reporting is proposed.

- Active users and returning users, with explicit definitions and date ranges.
- New creators, published posts, active groups, discussions/replies, and events hosted.
- RSVP conversion, payment conversion, cancellation rate, and attendance where measured.
- Refund rate, payout completion time, provider charges, and earned platform revenue.
- Verification turnaround, moderation backlog, appeal outcomes, and support response time.
- Search success, broken links, rendering errors, and page performance where measured.
- Read-only exports and saved reports with role-based access.

Report actual captured/paid values separately from estimates. Do not fabricate visits, attendance, or revenue when tracking is missing. Do not call all collected ticket money platform revenue.

### 6.15 System operations and reliability

**Existing backend:** separate system routes, asynchronous services, search/storage/database integrations, and legacy admin operations. The current admin app has no complete operations console.

**Add:**

- Real service health, error rate, latency, queue backlog, and last successful job times.
- Post publication, indexing, file cleanup, notification, refund, and payout job states.
- Dead-letter jobs: failed messages kept for inspection rather than discarded.
- Safe retry, cancellation, and replay tools with a preview and duplicate protection.
- Database/search/storage consistency checks with approved repair jobs.
- Backup age, backup result, and tested restoration history.
- Approved maintenance actions with owner, reason, scope, and expiry.
- Feature flags and limits with staging preview, change history, and rollback.
- Incident timeline and links to logs/traces with sensitive data removed.

Avoid a browser form that executes arbitrary SQL, shell commands, or SSH commands. Use reviewed operations with a fixed purpose and narrow permissions. Existing legacy backup routes should not be turned into unrestricted controls for ordinary staff.

Some system endpoints acknowledge maintenance, cache-clear, or backup requests without performing the stated operation. Runtime metrics are a different case and may be real. Each new operations screen must show actual execution and completion evidence, not merely an API's success message. Internal service access also needs protection because several services trust identities supplied by the gateway.

### 6.16 Platform settings and policy

**Proposed staff module.**

- Legal publisher identity, company contact details, and approved policy versions.
- Community guidelines and moderation decision categories.
- Paid-event fee, refund, payout, hold, and dispute policies with effective dates.
- Host onboarding requirements and supported payment currencies.
- Upload limits, rate limits, invitation limits, and abuse thresholds.
- Brand assets, notification templates, and approved homepage placements.
- Staff permission changes and approval thresholds.

A settings change must say who approved it, when it becomes effective, and which existing/new resources it affects. Historical financial records keep the rules recorded at purchase time.

### 6.17 Audit and exports

**Existing backend:** an admin audit table and writes for several staff actions. A searchable audit screen and complete coverage are proposed.

- Search by staff member, resource, action, date, and case/transaction reference.
- See before/after values, reason, approval, and result.
- Include failed/denied sensitive actions and protected-data access where appropriate.
- Distinguish staff action history from the financial ledger and user activity feed.
- Record export requests, who downloaded them, expiry, and retention.
- Provide a read-only audit role and protected retention storage.
- Never include passwords, session tokens, shared secrets, or full identity/bank details in ordinary logs.

### 6.18 Future products

**Future, not core first-release requirements.**

- Live streams: sessions, hosts, access rights, stream health, reported content, recordings, and shutdown decisions.
- Podcasts: shows, episodes, audio files, transcripts, publishing schedules, rights claims, and discovery controls.
- Question-and-answer features: questions, answers, accepted answers, tags, reputation abuse, and moderation history.
- Paid memberships/subscriptions: plans, renewals, cancellations, entitlements, refunds, and separate recurring-payment accounting.
- Sponsorships or promoted placements: approval, targeting, disclosure, billing, and performance reports.

These modules should reuse staff permissions, reporting, audit, job monitoring, and finance foundations. Do not present their screens as working products before their backend and user flows exist.

## 7. What staff should see in the navigation

Show only sections the staff member is permitted to use:

1. Overview and my work.
2. Finance: paid events, transactions, refunds, host payouts, holds, reconciliation, statements.
3. Accounts and staff access.
4. Verification.
5. Posts and authors.
6. Discussions and replies.
7. Events and attendance.
8. Groups and membership.
9. Reports and appeals.
10. Support cases.
11. Notifications and templates.
12. Media and creator tools.
13. Topics, discovery, and homepage placements.
14. SEO and feeds.
15. Analytics.
16. System operations.
17. Audit history and exports.
18. Settings and policy.

Future-product entries stay hidden until their capabilities are available. Menu visibility is a convenience; server permission checks remain mandatory.

## 8. Experience on phone, tablet, and desktop

- Phone: reachable menu, simple work queues, summary cards, full-screen record details, readable amounts, and visible logout.
- Tablet: list and detail where they fit; collapse into separate screens when needed.
- Desktop: searchable lists with detail panels and comparison views.
- Use Monkeys colours sparingly for active controls and clear status indicators.
- Use text labels as well as colour for held, failed, pending, and completed states.
- Keep table headers, filters, date range, currency, and totals clear.
- Paginate or load long histories incrementally. Never download every account or payment to render the first screen.
- Display timezone and currency explicitly; store stable timestamps for audit.
- Require confirmation that describes the target, amount, and consequence of a sensitive action.
- For a payout, show event, payee, masked destination, amount, deductions, and approval evidence together before execution/recording.
- Support keyboard navigation, screen readers, readable contrast, and usable touch targets.
- Show loading, unavailable, empty, permission-denied, and failed states distinctly.
- Preserve search/filter state when staff return from a record.
- Protect unsaved notes from accidental navigation and prevent double submissions.
- Make private documents deliberately opened, rather than automatically visible to everybody near a staff screen.

## 9. Recommended approach and backend work

### Approach choices

**Recommended: extend the existing separate Svelte admin app and its staff APIs.** This keeps the panel focused, preserves working integrations, and allows finance, moderation, and support to be delivered separately.

**Alternative: rebuild the admin app in the public frontend stack.** This offers more shared UI code but also adds migration work. It does not remove the need for new finance and permission APIs. Keep deployment and access separate even if packages are shared.

**Alternative: use provider/accounting dashboards for most finance operations and make Monkeys admin mainly read-only.** This can reduce custom transfer work, but event-specific host balances, release approvals, complaints, and connected support still need Monkeys records.

Start with the recommended approach and manual transfers plus reconciliation. Add automated transfers only after the ledger, permission model, failure recovery, and actual provider account capabilities have been verified.

### Backend work is required

The current APIs are useful foundations, but the detailed panel cannot be built by changing the admin UI alone.

- **Gateway:** explicit staff-session permissions, finance roles, guarded exports, protected document access, and new staff routes.
- **Events service:** unified cancellation/refund jobs, ticket transaction history, release eligibility, holds, refund outcomes, and event-linked payout data.
- **Finance component:** append-only accounting entries, approvals, transfer attempts, provider/bank reconciliation, adjustments, and statements. Decide in its implementation design whether it starts as a clear module in Events or a separate service; do not create two competing ledgers.
- **Users/auth services:** staff access lifecycle, session revocation, account restrictions, and verification decisions.
- **Posts/discussions/groups services:** authorised moderation, case-linked private previews, restoration, and consistent visibility changes.
- **Storage:** restricted evidence, expiry, retention, quarantines, and reference-safe cleanup.
- **Notifications/activity:** reliable messages and linked action history; they must not become the source of financial balances.
- **Database migrations:** additive tables/indexes, retained identifiers, permission migration, and preserved historical payment records.

The admin browser never connects directly to the databases or computes authoritative payout balances. Each service validates its actions, and the ledger owns the money calculations.

## 10. Delivery order

### Phase 0: establish trustworthy access and records

- Version the admin app in an agreed repository.
- Replace inferred roles with explicit server permissions.
- Separate finance permissions from Community moderation.
- Restrict the legacy verification write route, enforce suspension, and close direct-media audience gaps.
- Define protected-document access, staff session revocation, audit coverage, and the mobile menu.
- Confirm the fee/refund/payout policies and provider account arrangement.

### Phase 1: paid-event finance and manual release

- Correct shared event cancellation/refund processing.
- Build the event finance detail, transaction history, and visible reservation/paid balances.
- Build host payment profiles, holds, partial payout requests, separate approval, bank evidence, and reconciliation.
- Add durable refund jobs, confirmed refund outcomes, negative-balance cases, and duplicate protection.
- Build the permanent accounting ledger and host/event statements.

**Phase 1 is the first product priority.** No automatic host release is required for this phase.

### Phase 2: moderation and connected support

- Unified reports, reviewer assignment, evidence, warnings/restrictions, restoration, and appeals.
- Discussion/reply administration.
- Complete account, post, group, event, and verification details.
- Support cases linked to users and finance records.

### Phase 3: discovery and operations

- Topic management, homepage curation, SEO/feed checks, and search repair jobs.
- Notification delivery, media inventory, operational monitoring, and controlled retries.
- Detailed analytics and permission-controlled exports.

### Phase 4: automation and future products

- Provider-integrated host transfers with the same approval and reconciliation rules.
- Scheduled reporting and approved low-risk automation.
- Podcast, live-stream, Q&A, or subscription modules after their product flows are built.

Each phase needs its own focused implementation specification, API contracts, and verification plan. This document is the overall feature plan, not approval to build every module at once.

## 11. Acceptance checks before financial use

1. A normal user, event host, or group moderator cannot open staff data or call a finance action directly.
2. Community/moderation staff cannot release money without an explicit finance permission.
3. Two staff submitting the same payout cannot reserve or pay the same balance twice.
4. The person preparing a payout cannot quietly approve it themselves.
5. A refund arriving after approval forces the payout balance to be reviewed again.
6. A cancelled paid event creates refund work whether cancelled by its host or by staff.
7. A service restart preserves unfinished refund and payout work.
8. A timed-out provider/bank operation is investigated before a second transfer is sent.
9. Repeated or out-of-order callbacks do not duplicate money or regress a completed transaction.
10. Provider amount, currency, and references are checked against the expected order.
11. A completed host payout has evidence, a verified destination, amount, reviewer, and time.
12. A refund or reversal after payout shows the debt/loss explicitly.
13. Original ledger entries survive corrections, username changes, event changes, and account retention actions.
14. Older captures and pre-ledger paid records are reconciled during migration without fabricated amounts or duplicate capture entries.
15. The opening ledger balances match independently checked provider/bank records.
16. Staff can complete review tasks on phone, tablet, and desktop without inaccessible navigation.
17. Private documents, bank data, drafts, and meeting links stay out of public APIs and discovery output.
18. Exports and audit totals can be traced back to individual records.
19. A suspended account cannot keep posting or regain access through refresh, and a suspended group cannot republish itself.
20. Every legacy route that makes a staff decision has the same permission check as the current admin route.
21. Members-only media cannot be fetched by an unauthorised caller using its direct asset URL.

## 12. Business decisions to confirm before implementation

These choices are deliberately left for the owner to confirm. They do not block writing this feature plan.

1. Which repository should own and deploy the separate admin app?
2. Who may prepare, approve, execute, and reconcile payouts?
3. Is the default release date after event completion, and what review/hold period applies?
4. Are partial releases allowed? Are advance releases allowed under a recorded exception?
5. What are the current agreed fee, tax, provider-charge, and invoice rules?
6. Who bears each charge when a ticket is refunded or disputed?
7. What reserve should be kept, and when can it be released?
8. Which bank/payout destinations and currencies are supported at launch?
9. What evidence is required to confirm a transfer, and who matches it to the bank?
10. What are the host-debt recovery rules after a payout has already happened?
11. Which staff may view verification documents and private-group content, and for what reasons?
12. What retention periods apply to financial history, private evidence, and exported files?
13. Which provider product and business account is approved for eventual host transfer automation?

Until these choices are confirmed, use the recommended manual-transfer design as a proposal, not an enabled financial policy.

## 13. Useful daily examples

### A paid event finishes normally

Finance opens the event, checks collected tickets and refunds, checks bank funds, prepares a release, obtains approval, records the transfer, matches the reference, and sends the host a statement.

### A host cancels a paid event

The panel shows cancellation and all affected attendees. Refund work is queued. Host release is held. Staff see which refunds are processing, failed, or completed and can investigate the failed ones.

### A transfer result is unclear

The payout stays in an investigation state with its amount reserved. Staff check the bank/provider reference. The panel does not create a second transfer merely because the first request timed out.

### A refund arrives after the host was paid

The original payout remains in history. A refund and its host adjustment are added. The host's debt or the platform's loss is shown, with the approved recovery decision.

### A member reports abuse in a private group

A permitted reviewer opens a case-limited preview, records a decision, applies the permitted action, and informs the affected people. Other staff cannot browse the private group merely because they work for the platform.

## 14. Source map for the engineering follow-up

These paths are the main evidence for the findings above. Current code takes precedence where older handoff documents disagree.

### Admin app

- `local/monkeys-admin/src/App.svelte`: current screens and staff shell.
- `local/monkeys-admin/src/lib/session.ts`: inferred staff session/role handling.
- `local/monkeys-admin/src/lib/api.ts`: cookie API requests and role navigation.
- `local/monkeys-admin/src/pages/Stats.svelte`: overview counters.
- `local/monkeys-admin/src/pages/Users.svelte`: account actions.
- `local/monkeys-admin/src/pages/Blogs.svelte`: current post list and takedowns.
- `local/monkeys-admin/src/pages/Events.svelte`: staff event actions.
- `local/monkeys-admin/src/pages/Groups.svelte`: staff group actions.
- `local/monkeys-admin/src/pages/Verifications.svelte`: verification queue and evidence.
- `local/monkeys-admin/src/pages/Payments.svelte`: current manual settlement bookkeeping.
- `local/monkeys-admin/src/pages/Moderation.svelte`: current flags and comment/question removal.
- `local/monkeys-admin/src/components/SideNav.svelte` and `local/monkeys-admin/src/app.css`: navigation and responsive layout.

### Staff APIs and financial records

- `microservices/the_monkeys_gateway/internal/admin/routes.go`: registered access rules and legacy handlers.
- `microservices/the_monkeys_gateway/internal/admin/rbac.go`: role middleware.
- `microservices/the_monkeys_gateway/internal/admin/stats.go`: role-dependent summary data.
- `microservices/the_monkeys_gateway/internal/admin/payments.go`: finance API inputs and output fields.
- `microservices/the_monkeys_gateway/internal/storage_v2/verification.go`: protected verification access.
- `microservices/the_monkeys_gateway/internal/auth/routes.go` and `microservices/the_monkeys_authz/internal/services/user_verification.go`: legacy verification review permissions.
- `microservices/the_monkeys_authz/internal/services/services.go`: login, refresh, and token validation.
- `microservices/the_monkeys_users/internal/database/admin.go`: role and account status changes.
- `microservices/the_monkeys_groups/internal/database/groups.go`: group status transitions.
- `microservices/the_monkeys_storage/internal/database/database.go` and `microservices/the_monkeys_gateway/internal/storage_v2/visibility.go`: asset permissions and caching.
- `microservices/the_monkeys_gateway/internal/systems/routes.go`: system-operation implementations.
- `microservices/the_monkeys_events/internal/database/admin_payments.go`: pending/paid settlements and audit writes.
- `microservices/the_monkeys_events/internal/database/admin_catalog.go`: staff cancellation and payment counters.
- `microservices/the_monkeys_events/internal/database/admin_moderation.go`: flags and deletion-backed hide actions.
- `microservices/the_monkeys_events/internal/database/attendees.go`: capture/refund records and RSVP changes.
- `microservices/the_monkeys_events/internal/database/events.go`: financial deletion restrictions.
- `microservices/the_monkeys_events/internal/services/service.go`: host/staff cancellation and payment callbacks.
- `microservices/the_monkeys_events/internal/services/payments.go`: provider order/refund client.
- `microservices/the_monkeys_events/internal/money/money.go`: current fee split and zero-clamped open payable.
- `schema/000018_event_payments.up.sql`: capture and settlement tables.
- `schema/000019_admin_moderation.up.sql`: flags and audit tables.
- `schema/000021_event_host_review.up.sql`: attendee screening.
- `schema/000022_blog_group_audience.up.sql` and `schema/000024_discussion_group_audience.up.sql`: group audience rules.
- `docs/admin-api.md` and `docs/frontend-admin-dashboard.md`: earlier handoffs, with role/cancellation assumptions requiring updates.
- `.gitignore`: `local/*` exclusion.

### Public product

- `local/the_monkeys/apps/the_monkeys/src/services/events/`: event API and types.
- `local/the_monkeys/apps/the_monkeys/src/components/events/EventForm.tsx`: ticket-creation restrictions.
- `local/the_monkeys/apps/the_monkeys/src/components/events/EventManage.tsx`: priced-tier management, coupons, and attendees.
- `local/the_monkeys/apps/the_monkeys/src/components/events/RsvpPanel.tsx`: RSVP and checkout.
- `local/the_monkeys/apps/the_monkeys/src/services/groups/`: group API and types.
- `local/the_monkeys/apps/the_monkeys/src/services/discussions/`: discussion API and types.
- `local/the_monkeys/apps/the_monkeys/src/services/blog/`: post API, publication scope, and types.
- `local/the_monkeys/apps/the_monkeys/src/services/cards/`: digital business cards.
- `local/the_monkeys/apps/the_monkeys/src/services/verification/`: account verification.
- `local/the_monkeys/apps/the_monkeys/src/features/snapshot/`: social snapshot tools.
- `local/the_monkeys/apps/the_monkeys/src/app/`: actual public routes, metadata, sitemaps, and feeds.

## 15. Provider references used for the finance design

- [Razorpay: About settlements](https://razorpay.com/docs/payments/settlements/): provider-to-bank settlement is separate from capture and from our host payment records.
- [Razorpay: Refund webhook events](https://razorpay.com/docs/payments/refunds/subscribe-to-webhooks/): refund creation, successful processing, and failure are separate events.

Account availability, transfer products, banking setup, tax treatment, and release terms must be checked for Buddhicintaka's actual business account before implementing automated money movement.
