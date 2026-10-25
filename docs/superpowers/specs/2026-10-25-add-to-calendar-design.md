# Add to calendar design

Approved direction: no-account-connection provider chooser with explicit import fallback. Work remains local on `codex/event-timezone-release` in both repositories. Preserve unrelated and attendee edits. Do not commit, push, access production or mutate event data.

## Goal and approach

Replace the ambiguous Calendar button with an accessible mobile-first Add to calendar dialog. Offer Google Calendar, Outlook and Apple Calendar. For recurring events let the user choose This occurrence (default) or Available series dates.

Provider compose links are appropriate for a single occurrence. Use UTC instants and a valid event timezone where supported; never reconstruct an instant from browser-local text. Include the Monkeys page link, not a private meeting link or attendee details, in provider URLs. Apple uses an ICS file. Series exports use ICS import for all providers with provider-specific instructions and clear desktop limitations for Google. No OAuth, subscriptions or automatic synchronization in this change.

The alternatives were importing files for every action (unnecessary friction for single Google/Outlook events), and full account integrations (permissions, credentials and synchronization outside the approved scope). The user selected no account connection with import fallback.

## Recurrence and compatibility

Monkeys materializes series dates as separate event rows. Export actual visible, published/live/completed occurrences from the selected occurrence's original series position onward. Retain individually edited instants, durations and locations. Do not generate imaginary dates from recurrence_text, expose unpublished/private sibling events, or claim an endless synced series. Cancelled dates are not imported as confirmed events.

Keep GET /api/v1/events/:slug/calendar unchanged by default: one occurrence and the existing ICS format/UID. Add optional scope=series, carried through an additive boolean CalendarReq.include_series field with default false. The user must generate protobuf code. A separate response header confirms series scope, so a newer UI cannot silently accept a single file from an older backend.

UTC DTSTART/DTEND for every concrete occurrence preserve the stored instant, including across daylight-saving boundaries, and work even when legacy timezone labels are invalid. Keep stable existing event UIDs, escape text/parameters, fold UTF-8 without splitting characters, validate timestamps, and include modification dates where available. Calendar reads must not geocode or update records.

## Privacy and boundaries

Preserve per-event visibility enforcement. Authenticate the export viewer through the existing optional session and forward the account ID, but never trust a browser-supplied account ID. Apply join-link redaction separately per occurrence. No meeting URL in provider links. No database migration or existing-data rewrite.

## Verification

Test legacy downloads, series opt-in, old-backend detection, authorization, cancelled/private/draft siblings, rescheduled exceptions, India/Europe/US date boundaries, DST transitions, missing/invalid legacy zones, UTF-8/ICS escaping, download failures and keyboard/mobile dialog behavior. Test with synthetic fixtures and offline guards. Inspect only local UI; never save real events to external calendar accounts during testing.
