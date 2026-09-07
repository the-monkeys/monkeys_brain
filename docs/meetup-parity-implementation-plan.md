# Meetup-Like Communities And Events Implementation Plan

## Purpose

This document defines how to evolve The Monkeys into a Meetup-like community and events platform while preserving the event service work currently in progress.

The current implementation already provides a strong event foundation:

- Event create, update, delete, publish, cancel, detail and listing APIs.
- Ticket tiers, coupons, RSVP, waitlist, payment order creation, Razorpay webhook handling and refunds.
- Event comments, reactions, reports, co-hosts, attendee listing/export, share metadata, calendar files and reminders.

The missing Meetup-class product surface is mostly around groups/communities, group membership, recurring events, advanced attendee management, registration forms, messaging, search/recommendations, subscriptions, organizer tooling, analytics and UI workflows.

The implementation must be backward compatible. Existing `/api/v1/events` behavior, protobuf field numbers, database semantics and current event routes must remain valid.

## Engineering Principles

- Keep changes additive whenever possible.
- Do not rename or reuse existing protobuf field numbers.
- Do not destructively edit migrations that may have already been applied. Add new migrations after `000010_add_events`.
- Keep data units consistent across the program.
- Store timestamps in UTC in the database.
- Accept and return RFC3339 timestamps at REST boundaries.
- Use protobuf `google.protobuf.Timestamp` for gRPC time fields.
- Store money as integer minor units where possible for new payment tables, for example paise/cents, instead of floating point.
- Keep code clean, DRY, modular and fast.
- Keep containers multi-stage, small and fast to build.
- Add authn, authz and rate limits before exposing any new route.
- Preserve old event behavior while adding group-scoped behavior.

## Execution Order

Implementation should happen in this exact order:

1. Database schema migrations.
2. gRPC protobuf additions.
3. Database layer.
4. Service layer.
5. API gateway layer.
6. Authn, authz and rate-limit wiring.
7. UI implementation and API wiring.
8. Tests, build verification and backward compatibility checks.

## Phase 0: Protect Existing Work ☑️

Before feature coding starts:

- Create a checkpoint branch or commit for the current event-service work.
- Run the existing Go unit tests and build.
- Add focused tests around existing event behavior:
  - Create draft event.
  - Publish event.
  - Free RSVP.
  - Paid RSVP pending payment.
  - Razorpay webhook confirmation.
  - Waitlist creation and promotion.
  - Cancel event and refund path.
  - Co-host permission checks.
  - Meeting link redaction for non-attendees.
- Freeze existing REST contracts unless a versioned endpoint is introduced.
- Add compatibility tests for existing `/api/v1/events` routes.

## Phase 1: Database Schema Level ☑️

### General Migration Rules

- Add migrations as `schema/000011_*.up.sql`, `schema/000011_*.down.sql`, etc.
- Keep `events.id`, `events.slug` and existing `event_attendees` semantics intact.
- Add nullable columns first, backfill data, then add stricter constraints later.
- Prefer `BIGINT` foreign keys because the current schema uses `BIGSERIAL`.
- Add indexes for every expected list/filter/join path.
- Use soft-delete/status fields for user-facing resources where audit/history matters.

### Groups And Communities

Add a group/community layer above events.

New tables:

```sql
groups (
    id BIGSERIAL PRIMARY KEY,
    slug VARCHAR(255) NOT NULL UNIQUE,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    visibility VARCHAR(30) NOT NULL DEFAULT 'public',
    status VARCHAR(30) NOT NULL DEFAULT 'draft',
    city VARCHAR(120),
    region VARCHAR(120),
    country VARCHAR(120),
    timezone VARCHAR(64) NOT NULL DEFAULT 'UTC',
    latitude DECIMAL(9,6),
    longitude DECIMAL(9,6),
    cover_image TEXT,
    logo_image TEXT,
    organizer_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    member_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

```sql
group_members (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    role VARCHAR(40) NOT NULL DEFAULT 'member',
    status VARCHAR(40) NOT NULL DEFAULT 'active',
    joined_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (group_id, user_id)
);
```

```sql
group_permissions (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    permission_type VARCHAR(80) NOT NULL,
    UNIQUE (group_id, user_id, permission_type)
);
```

```sql
group_topics (
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    topic_name VARCHAR(100) NOT NULL,
    PRIMARY KEY (group_id, topic_name)
);
```

```sql
group_join_requests (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    answers JSONB,
    status VARCHAR(40) NOT NULL DEFAULT 'pending',
    decided_by BIGINT REFERENCES user_account(id) ON DELETE SET NULL,
    decided_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (group_id, user_id)
);
```

```sql
group_bans (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    reason TEXT,
    banned_by BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (group_id, user_id)
);
```

```sql
group_rules (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    title VARCHAR(120) NOT NULL,
    body TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

Indexes:

- `idx_groups_status`
- `idx_groups_location` on `country, region, city`
- `idx_groups_geo` on `latitude, longitude`
- `idx_group_members_user`
- `idx_group_members_group_status`
- `idx_group_topics_topic`
- `idx_group_join_requests_group_status`

Modify existing `events` table:

```sql
ALTER TABLE events ADD COLUMN group_id BIGINT NULL REFERENCES groups(id) ON DELETE SET NULL;
ALTER TABLE events ADD COLUMN visibility VARCHAR(30) NOT NULL DEFAULT 'public';
ALTER TABLE events ADD COLUMN rsvp_opens_at TIMESTAMP NULL;
ALTER TABLE events ADD COLUMN rsvp_closes_at TIMESTAMP NULL;
ALTER TABLE events ADD COLUMN allow_guests BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE events ADD COLUMN max_guests_per_rsvp INTEGER NOT NULL DEFAULT 0;
ALTER TABLE events ADD COLUMN venue_id BIGINT NULL;
ALTER TABLE events ADD COLUMN how_to_find_us TEXT;
```

Keep `group_id` nullable so existing standalone events still work.

### Venues And Locations

New tables:

```sql
venues (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    address_line1 VARCHAR(255),
    address_line2 VARCHAR(255),
    city VARCHAR(120),
    region VARCHAR(120),
    country VARCHAR(120),
    postal_code VARCHAR(40),
    latitude DECIMAL(9,6),
    longitude DECIMAL(9,6),
    source VARCHAR(40) NOT NULL DEFAULT 'manual',
    created_by BIGINT REFERENCES user_account(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

After the venues table exists:

```sql
ALTER TABLE events
    ADD CONSTRAINT fk_events_venue
    FOREIGN KEY (venue_id) REFERENCES venues(id) ON DELETE SET NULL;
```

Indexes:

- `idx_venues_location`
- `idx_venues_geo`
- `idx_venues_name_trgm` if trigram search is enabled.

### Recurring Events

New tables:

```sql
event_series (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL,
    organizer_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    title VARCHAR(255) NOT NULL,
    description TEXT,
    timezone VARCHAR(64) NOT NULL DEFAULT 'UTC',
    recurrence_rule TEXT NOT NULL,
    recurrence_starts_at TIMESTAMP NOT NULL,
    recurrence_ends_at TIMESTAMP NULL,
    status VARCHAR(40) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

Modify `events`:

```sql
ALTER TABLE events ADD COLUMN series_id BIGINT NULL REFERENCES event_series(id) ON DELETE SET NULL;
ALTER TABLE events ADD COLUMN series_occurrence_at TIMESTAMP NULL;
```

Rules:

- Generated events must be normal rows in `events`.
- Editing one occurrence updates only that event row.
- Editing a series updates future unmodified generated events.
- Cancelling one occurrence should not cancel the whole series.

### Registration Forms And Attendee Questions

New tables:

```sql
event_questions (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    question_text TEXT NOT NULL,
    question_type VARCHAR(40) NOT NULL DEFAULT 'text',
    required BOOLEAN NOT NULL DEFAULT FALSE,
    options JSONB,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

```sql
event_question_answers (
    id BIGSERIAL PRIMARY KEY,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    attendee_id BIGINT NOT NULL REFERENCES event_attendees(id) ON DELETE CASCADE,
    question_id BIGINT NOT NULL REFERENCES event_questions(id) ON DELETE CASCADE,
    answer JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (attendee_id, question_id)
);
```

Modify `event_attendees`:

```sql
ALTER TABLE event_attendees ADD COLUMN guest_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE event_attendees ADD COLUMN attendance_status VARCHAR(40) NOT NULL DEFAULT 'registered';
ALTER TABLE event_attendees ADD COLUMN checked_in_at TIMESTAMP NULL;
ALTER TABLE event_attendees ADD COLUMN checked_in_by BIGINT NULL REFERENCES user_account(id) ON DELETE SET NULL;
```

Do not remove the existing `checked_in` column. It remains for backward compatibility. New code should keep `checked_in` and `checked_in_at` consistent.

### Messaging And Discussions

New tables:

```sql
message_threads (
    id BIGSERIAL PRIMARY KEY,
    thread_type VARCHAR(40) NOT NULL,
    group_id BIGINT REFERENCES groups(id) ON DELETE CASCADE,
    event_id BIGINT REFERENCES events(id) ON DELETE CASCADE,
    created_by BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    title VARCHAR(255),
    status VARCHAR(40) NOT NULL DEFAULT 'active',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

```sql
message_thread_members (
    thread_id BIGINT NOT NULL REFERENCES message_threads(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    last_read_at TIMESTAMP,
    muted BOOLEAN NOT NULL DEFAULT FALSE,
    archived BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (thread_id, user_id)
);
```

```sql
messages (
    id BIGSERIAL PRIMARY KEY,
    thread_id BIGINT NOT NULL REFERENCES message_threads(id) ON DELETE CASCADE,
    sender_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    body TEXT NOT NULL,
    status VARCHAR(40) NOT NULL DEFAULT 'visible',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

### Subscriptions, Dues And Entitlements

New tables:

```sql
plans (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(80) NOT NULL UNIQUE,
    name VARCHAR(120) NOT NULL,
    plan_type VARCHAR(40) NOT NULL,
    price_minor BIGINT NOT NULL DEFAULT 0,
    currency VARCHAR(10) NOT NULL DEFAULT 'INR',
    billing_interval VARCHAR(40) NOT NULL DEFAULT 'month',
    active BOOLEAN NOT NULL DEFAULT TRUE,
    entitlements JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

```sql
organizer_subscriptions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    plan_id BIGINT NOT NULL REFERENCES plans(id) ON DELETE RESTRICT,
    status VARCHAR(40) NOT NULL DEFAULT 'active',
    provider VARCHAR(40) NOT NULL DEFAULT 'razorpay',
    provider_subscription_id VARCHAR(255),
    current_period_start TIMESTAMP,
    current_period_end TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

```sql
group_dues (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    amount_minor BIGINT NOT NULL,
    currency VARCHAR(10) NOT NULL DEFAULT 'INR',
    billing_interval VARCHAR(40) NOT NULL DEFAULT 'month',
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

```sql
group_due_payments (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    amount_minor BIGINT NOT NULL,
    currency VARCHAR(10) NOT NULL DEFAULT 'INR',
    provider VARCHAR(40) NOT NULL DEFAULT 'razorpay',
    provider_payment_id VARCHAR(255),
    status VARCHAR(40) NOT NULL DEFAULT 'pending',
    period_start TIMESTAMP,
    period_end TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

### Search, Saves And Recommendations

New tables:

```sql
user_interests (
    user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    topic_name VARCHAR(100) NOT NULL,
    PRIMARY KEY (user_id, topic_name)
);
```

```sql
saved_events (
    user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, event_id)
);
```

```sql
saved_groups (
    user_id BIGINT NOT NULL REFERENCES user_account(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, group_id)
);
```

Search can start with Postgres full-text/trigram indexes and later sync to Elasticsearch/OpenSearch.

### Analytics

New tables:

```sql
event_analytics_daily (
    event_id BIGINT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    day DATE NOT NULL,
    views INTEGER NOT NULL DEFAULT 0,
    shares INTEGER NOT NULL DEFAULT 0,
    saves INTEGER NOT NULL DEFAULT 0,
    rsvps INTEGER NOT NULL DEFAULT 0,
    cancellations INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (event_id, day)
);
```

```sql
group_analytics_daily (
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    day DATE NOT NULL,
    views INTEGER NOT NULL DEFAULT 0,
    joins INTEGER NOT NULL DEFAULT 0,
    leaves INTEGER NOT NULL DEFAULT 0,
    event_views INTEGER NOT NULL DEFAULT 0,
    event_rsvps INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (group_id, day)
);
```

## Phase 2: gRPC Protobuf Level ☑️

### Proto Rules

- Keep `gateway_event/pb/gw_event.proto` backward compatible.
- Do not renumber existing fields.
- Add new fields with new numbers only.
- Prefer new request/response messages for new features.
- Consider adding a new `gateway_group/pb/gw_group.proto` if the group surface grows large. This is preferred for modularity.
- Generate Go code only after the user runs `protoc`.

### Event Proto Additions

Extend `Event` with optional, additive fields:

```proto
int64 group_id = 23;
string group_slug = 24;
string group_name = 25;
string visibility = 26;
google.protobuf.Timestamp rsvp_opens_at = 27;
google.protobuf.Timestamp rsvp_closes_at = 28;
bool allow_guests = 29;
int32 max_guests_per_rsvp = 30;
int64 venue_id = 31;
Venue venue = 32;
string how_to_find_us = 33;
int64 series_id = 34;
google.protobuf.Timestamp series_occurrence_at = 35;
repeated EventQuestion questions = 36;
```

Extend `CreateEventReq` and `UpdateEventReq` with the same additive fields using new field numbers.

Add messages:

```proto
message Venue {
    int64 id = 1;
    string name = 2;
    string address_line1 = 3;
    string address_line2 = 4;
    string city = 5;
    string region = 6;
    string country = 7;
    string postal_code = 8;
    double latitude = 9;
    double longitude = 10;
}
```

```proto
message EventQuestion {
    int64 id = 1;
    int64 event_id = 2;
    string question_text = 3;
    string question_type = 4;
    bool required = 5;
    string options_json = 6;
    int32 sort_order = 7;
}
```

```proto
message RSVPAnswer {
    int64 question_id = 1;
    string answer_json = 2;
}
```

Extend `RSVPReq`:

```proto
int32 guest_count = 6;
repeated RSVPAnswer answers = 7;
```

Keep old RSVP clients working by defaulting `guest_count` to `0` and allowing empty answers unless required questions exist.

Add attendee management RPCs:

```proto
message UpdateAttendanceReq {
    string event_slug = 1;
    string account_id = 2;
    int64 attendee_id = 3;
    string attendance_status = 4;
    bool checked_in = 5;
    ClientInfo client_info = 6;
}

rpc UpdateAttendance(UpdateAttendanceReq) returns (BasicResp);
```

Add save APIs:

```proto
message SaveEventReq {
    string event_slug = 1;
    string account_id = 2;
    ClientInfo client_info = 3;
}

rpc SaveEvent(SaveEventReq) returns (BasicResp);
rpc UnsaveEvent(SaveEventReq) returns (BasicResp);
```

### Group Proto

Create `apis/serviceconn/gateway_group/pb/gw_group.proto`.

Core messages:

```proto
message Group {
    int64 id = 1;
    string slug = 2;
    string name = 3;
    string description = 4;
    string visibility = 5;
    string status = 6;
    string city = 7;
    string region = 8;
    string country = 9;
    string timezone = 10;
    double latitude = 11;
    double longitude = 12;
    string cover_image = 13;
    string logo_image = 14;
    string organizer_account_id = 15;
    string organizer_username = 16;
    int32 member_count = 17;
    repeated string topics = 18;
    google.protobuf.Timestamp created_at = 19;
    google.protobuf.Timestamp updated_at = 20;
}
```

```proto
message CreateGroupReq {
    string account_id = 1;
    string name = 2;
    string description = 3;
    string visibility = 4;
    string city = 5;
    string region = 6;
    string country = 7;
    string timezone = 8;
    double latitude = 9;
    double longitude = 10;
    string cover_image = 11;
    string logo_image = 12;
    repeated string topics = 13;
    ClientInfo client_info = 14;
}
```

Required RPCs:

```proto
service GroupService {
    rpc CreateGroup(CreateGroupReq) returns (GroupResp);
    rpc UpdateGroup(UpdateGroupReq) returns (GroupResp);
    rpc DeleteGroup(GroupActionReq) returns (BasicResp);
    rpc PublishGroup(GroupActionReq) returns (GroupResp);
    rpc GetGroup(GetGroupReq) returns (GroupResp);
    rpc ListGroups(ListGroupsReq) returns (ListGroupsResp);

    rpc JoinGroup(JoinGroupReq) returns (BasicResp);
    rpc LeaveGroup(GroupActionReq) returns (BasicResp);
    rpc ListMembers(ListGroupMembersReq) returns (ListGroupMembersResp);
    rpc UpdateMemberRole(UpdateMemberRoleReq) returns (BasicResp);
    rpc RemoveMember(UpdateMemberRoleReq) returns (BasicResp);
    rpc BanMember(UpdateMemberRoleReq) returns (BasicResp);
    rpc ApproveJoinRequest(JoinDecisionReq) returns (BasicResp);
    rpc RejectJoinRequest(JoinDecisionReq) returns (BasicResp);

    rpc AddGroupRule(GroupRuleReq) returns (GroupRuleResp);
    rpc UpdateGroupRule(GroupRuleReq) returns (GroupRuleResp);
    rpc DeleteGroupRule(GroupRuleActionReq) returns (BasicResp);

    rpc Authorize(AuthorizeGroupReq) returns (AuthorizeGroupResp);
}
```

### Messaging Proto

Messaging can be a separate `gateway_message/pb/gw_message.proto` or a group-service subsection. Prefer a separate service if direct messages and group discussions are both included.

Required RPCs:

- `CreateThread`
- `ListThreads`
- `GetThread`
- `SendMessage`
- `ListMessages`
- `MarkThreadRead`
- `MuteThread`
- `ArchiveThread`
- `ReportMessage`
- `DeleteMessage`

### Subscription Proto

Create `gateway_subscription/pb/gw_subscription.proto` only when billing work starts.

Required RPCs:

- `ListPlans`
- `GetCurrentSubscription`
- `CreateOrganizerSubscriptionOrder`
- `ProcessSubscriptionWebhook`
- `CreateGroupDues`
- `CreateGroupDuesPayment`
- `ProcessDuesWebhook`

## Phase 3: Database Layer ☑️

### Package Structure

Keep each domain modular:

```text
microservices/the_monkeys_groups/
  internal/database/
    interface.go
    postgres.go
    groups.go
    members.go
    permissions.go
    rules.go
    search.go
  internal/services/
    service.go
    authorization.go
```

For events, extend existing files with focused additions:

```text
microservices/the_monkeys_events/internal/database/
  venues.go
  recurring.go
  questions.go
  attendance.go
  saved.go
```

### DB Layer Requirements

- Every public method accepts `context.Context`.
- Use transactions for multi-table writes.
- Use row locks for capacity, waitlist, payment and member-count mutations.
- Use parameterized SQL only.
- Keep hydration batched to avoid N+1 queries.
- Use small interfaces per service domain.
- Keep authorization checks inside DB mutations even if gateway already checks them.
- Use cursor pagination for heavy lists in later phases; offset is acceptable for first version only where current APIs already use it.

### Required DB Methods

Groups:

- `CreateGroup`
- `UpdateGroup`
- `SetGroupStatus`
- `DeleteGroup`
- `GetGroup`
- `ListGroups`
- `JoinGroup`
- `LeaveGroup`
- `ListMembers`
- `UpdateMemberRole`
- `RemoveMember`
- `BanMember`
- `ApproveJoinRequest`
- `RejectJoinRequest`
- `AuthorizeGroup`

Events additions:

- `CreateVenue`
- `SearchVenues`
- `AttachVenueToEvent`
- `CreateEventQuestions`
- `ReplaceEventQuestions`
- `SaveEvent`
- `UnsaveEvent`
- `UpdateAttendance`
- `CreateSeries`
- `GenerateSeriesOccurrences`
- `CancelSeriesOccurrence`
- `UpdateSeriesFutureOccurrences`

Messaging:

- `CreateThread`
- `ListThreads`
- `GetThread`
- `SendMessage`
- `ListMessages`
- `MarkRead`
- `MuteThread`
- `ArchiveThread`
- `DeleteMessage`

## Phase 4: Service Layer ☑️

### Service Requirements

- Service layer owns business rules and notifications.
- DB layer owns persistence and final permission enforcement.
- Gateway owns HTTP parsing and early auth/rate-limit rejection.
- Keep business rules deterministic and unit-testable.

### Group Service Business Rules

- Only verified users can create public groups if product policy requires it.
- A group organizer automatically becomes an active member.
- Organizer receives all group permissions.
- Co-organizers/moderators receive explicit permission rows.
- Private groups create join requests instead of immediate membership.
- Banned users cannot join or RSVP to group-only events.
- Group deletion should be restricted when active paid events exist.

### Event Service Additions

- If `group_id` is present, event creator must have group event creation permission.
- Group members can see member-only events.
- Public users can only see public published events.
- RSVP must enforce:
  - RSVP open/close time.
  - Guest count limits.
  - Required question answers.
  - Group membership requirement when applicable.
  - Ban checks.
- Check-in must update both `checked_in` and `checked_in_at`.
- Recurring event generation must be idempotent.

### Messaging Service Rules

- Direct messages require both users to be eligible under privacy settings.
- Group discussions require group membership.
- Event host announcements require event host permissions.
- Muted/archived thread state is per user.
- Message deletes should soft-delete user-facing content.

## Phase 5: API Gateway Level ☑️

### Route Structure

Keep existing event routes:

```text
/api/v1/events
```

Add group routes:

```text
GET    /api/v1/groups
POST   /api/v1/groups
GET    /api/v1/groups/:slug
PUT    /api/v1/groups/:slug
DELETE /api/v1/groups/:slug
POST   /api/v1/groups/:slug/publish
POST   /api/v1/groups/:slug/join
DELETE /api/v1/groups/:slug/membership
GET    /api/v1/groups/:slug/members
PUT    /api/v1/groups/:slug/members/:username/role
DELETE /api/v1/groups/:slug/members/:username
POST   /api/v1/groups/:slug/members/:username/ban
GET    /api/v1/groups/:slug/events
POST   /api/v1/groups/:slug/events
```

Add event route extensions:

```text
POST   /api/v1/events/:slug/save
DELETE /api/v1/events/:slug/save
PUT    /api/v1/events/:slug/attendees/:id/attendance
POST   /api/v1/events/:slug/questions
PUT    /api/v1/events/:slug/questions
POST   /api/v1/events/:slug/copy
POST   /api/v1/events/:slug/series/cancel-occurrence
```

Add venue routes:

```text
GET    /api/v1/venues
POST   /api/v1/venues
GET    /api/v1/venues/:id
```

Add messaging routes:

```text
GET    /api/v1/messages/threads
POST   /api/v1/messages/threads
GET    /api/v1/messages/threads/:id
GET    /api/v1/messages/threads/:id/messages
POST   /api/v1/messages/threads/:id/messages
POST   /api/v1/messages/threads/:id/read
POST   /api/v1/messages/threads/:id/mute
POST   /api/v1/messages/threads/:id/archive
DELETE /api/v1/messages/:id
```

Add discovery routes:

```text
GET /api/v1/discovery/events
GET /api/v1/discovery/groups
GET /api/v1/discovery/recommended/events
GET /api/v1/discovery/recommended/groups
```

### Gateway Requirements

- Keep request body structs per route group.
- Validate enum values at the HTTP boundary.
- Convert REST timestamps to protobuf timestamps in one helper.
- Return consistent JSON error shape.
- Add pagination consistently: `limit`, `offset` for current style; later support `cursor`.
- Add response adapters only when needed. Avoid duplicating protobuf model structs unless the frontend needs a different shape.

## Phase 6: Authentication And Authorization Level ☑️

### Authn Requirements

- Public reads can use optional auth.
- All mutations require auth.
- Payment webhooks remain unauthenticated but must verify provider signatures.
- Internal service calls must keep service-level network isolation through Docker networks.

### Permission Model

Group permissions:

- `edit_group`
- `manage_members`
- `manage_events`
- `manage_discussions`
- `manage_dues`
- `manage_roles`
- `view_group_analytics`
- `delete_group`

Event permissions already exist and should be reused:

- `edit_event`
- `manage_attendees`
- `manage_tickets`
- `manage_coupons`
- `manage_hosts`

New event permissions:

- `manage_questions`
- `manage_checkins`
- `message_attendees`
- `view_event_analytics`

Messaging permissions:

- Direct thread member can read/send unless blocked/muted by policy.
- Group member can read group discussions.
- Group moderator can delete/report-moderate group messages.
- Event host can message attendees.

### Gateway Guards

Add guards equivalent to the existing event `authx` guard:

```text
groups/authx/
  perm.go
  cache.go
  guard.go
```

Guard methods:

- `RequireGroupVisible`
- `RequireGroupMember`
- `RequireGroupPermission(permission)`
- `RequireGroupOrganizer`
- `RequireCanManageMember`

Event guard additions:

- `RequireCanRSVP`
- `RequireCanManageQuestions`
- `RequireCanCheckIn`
- `RequireCanViewAttendeeContact`

### Authorization Cache

- Cache positive and negative authorize responses briefly, similar to events.
- Invalidate or expire quickly after role/member changes.
- Keep TTL small, for example 15-60 seconds.

## Phase 7: Docker Compose Level ☑️

### Services To Add

Add only the services that are needed for the current phase.

Phase 1 service:

```yaml
the_monkeys_groups:
  container_name: "the-monkeys-groups"
  build:
    context: .
    dockerfile: microservices/the_monkeys_groups/Dockerfile
  environment:
    - APP_ENV=${APP_ENV}
  ports:
    - "${MICROSERVICES_GROUPS_PORT}:${MICROSERVICES_GROUPS_INTERNAL_PORT}"
  networks:
    - monkeys-network
  depends_on:
    the_monkeys_db:
      condition: service_healthy
    the_monkeys_cache:
      condition: service_healthy
    rabbitmq:
      condition: service_healthy
  restart: always
```

Later services:

- `the_monkeys_messages`
- `the_monkeys_subscriptions`
- `the_monkeys_discovery` if recommendation/search grows beyond gateway calls.

### Gateway Dependencies

Update `the_monkeys_gateway` depends-on list to include:

- `the_monkeys_events`
- `the_monkeys_groups`
- Later: `the_monkeys_messages`, `the_monkeys_subscriptions`, `the_monkeys_discovery`.

### Container Build Requirements

Every new Go service Dockerfile must be multi-stage:

```dockerfile
FROM golang:1.24-alpine AS builder
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/service ./microservices/the_monkeys_groups

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/service /service
USER nonroot:nonroot
ENTRYPOINT ["/service"]
```

Rules:

- Prefer distroless for production.
- Keep Alpine/dev Dockerfile available only if shell debugging is required.
- Do not install compilers or package managers in runtime images.
- Use `.dockerignore` to avoid copying `.git`, build output, test data and local artifacts.
- Keep build context at repo root only because shared packages are imported across services.

## Phase 8: Config And Environment Level ☑️

### `.env.example` Additions

Add group service config:

```env
MICROSERVICES_THE_MONKEYS_GROUPS=the_monkeys_groups
MICROSERVICES_GROUPS_PORT=50060
MICROSERVICES_GROUPS_INTERNAL_PORT=50060
```

Add optional services later:

```env
MICROSERVICES_THE_MONKEYS_MESSAGES=the_monkeys_messages
MICROSERVICES_MESSAGES_PORT=50061
MICROSERVICES_MESSAGES_INTERNAL_PORT=50061

MICROSERVICES_THE_MONKEYS_SUBSCRIPTIONS=the_monkeys_subscriptions
MICROSERVICES_SUBSCRIPTIONS_PORT=50062
MICROSERVICES_SUBSCRIPTIONS_INTERNAL_PORT=50062

MICROSERVICES_THE_MONKEYS_DISCOVERY=the_monkeys_discovery
MICROSERVICES_DISCOVERY_PORT=50063
MICROSERVICES_DISCOVERY_INTERNAL_PORT=50063
```

Add feature flags:

```env
FEATURE_GROUPS_ENABLED=true
FEATURE_GROUP_DUES_ENABLED=false
FEATURE_RECURRING_EVENTS_ENABLED=false
FEATURE_EVENT_QUESTIONS_ENABLED=false
FEATURE_MESSAGING_ENABLED=false
FEATURE_RECOMMENDATIONS_ENABLED=false
FEATURE_ORGANIZER_SUBSCRIPTIONS_ENABLED=false
```

Add product defaults:

```env
EVENT_DEFAULT_TIMEZONE=UTC
EVENT_DEFAULT_CURRENCY=INR
EVENT_MAX_GUESTS_PER_RSVP=5
EVENT_MAX_QUESTIONS=20
GROUP_MAX_TOPICS=10
GROUP_DEFAULT_VISIBILITY=public
```

Add geocoding/search config when implemented:

```env
GEO_PROVIDER=none
GEO_PROVIDER_API_KEY=
DISCOVERY_DEFAULT_RADIUS_KM=40
DISCOVERY_MAX_RADIUS_KM=250
```

Add payment config for subscriptions/dues:

```env
KEYS_RAZORPAY_SUBSCRIPTION_WEBHOOK_SECRET=
KEYS_RAZORPAY_DUES_WEBHOOK_SECRET=
```

RabbitMQ additions:

```env
RABBITMQ_QUEUES=...,to_groups_svc_queue,to_messages_svc_queue,to_subscriptions_svc_queue
RABBITMQ_ROUTING_KEYS=...,to_groups_svc_key,to_messages_svc_key,to_subscriptions_svc_key
```

### `config/config.go` Additions

Add strongly typed config fields:

- `Microservices.GroupsHost`
- `Microservices.GroupsPort`
- `Microservices.MessagesHost`
- `Microservices.MessagesPort`
- `Features.GroupsEnabled`
- `Features.GroupDuesEnabled`
- `Features.RecurringEventsEnabled`
- `Features.EventQuestionsEnabled`
- `Features.MessagingEnabled`
- `Features.RecommendationsEnabled`
- `Features.OrganizerSubscriptionsEnabled`
- `Events.DefaultTimezone`
- `Events.DefaultCurrency`
- `Events.MaxGuestsPerRSVP`
- `Events.MaxQuestions`
- `Groups.MaxTopics`
- `Groups.DefaultVisibility`
- `Discovery.DefaultRadiusKM`
- `Discovery.MaxRadiusKM`

Keep existing event config names intact.

### `config.yaml`

If a deployment uses `config.yaml`, mirror the same structure:

```yaml
features:
  groups_enabled: true
  group_dues_enabled: false
  recurring_events_enabled: false
  event_questions_enabled: false
  messaging_enabled: false
  recommendations_enabled: false
  organizer_subscriptions_enabled: false

events:
  default_timezone: UTC
  default_currency: INR
  max_guests_per_rsvp: 5
  max_questions: 20

groups:
  max_topics: 10
  default_visibility: public

discovery:
  default_radius_km: 40
  max_radius_km: 250
```

## Phase 9: UI Implementation

The UI should come after the backend APIs are stable enough to wire end-to-end.

### UI Requirements

- Follow the existing Monkeys theme.
- Keep pages responsive across mobile, tablet and desktop.
- Keep backward compatibility with existing event pages.
- Do not hide existing standalone event behavior when adding group events.
- Use consistent time and currency formatting everywhere.
- Prefer dense, useful product screens over marketing-like layouts.

### Required User Screens

Groups:

- Group discovery page.
- Group detail page.
- Create/edit group flow.
- Group members page.
- Join request review page.
- Group settings page.
- Group rules page.

Events:

- Group event creation flow.
- Standalone event creation flow remains supported.
- Event recurrence editor.
- Venue selector.
- RSVP form with guest count and questions.
- Attendee management page with check-in controls.
- Event analytics page.
- Save/share controls.

Messaging:

- Thread list.
- Direct message thread.
- Group discussion thread.
- Event attendee announcement composer.

Subscriptions:

- Organizer plan page.
- Billing status page.
- Group dues settings.
- Member dues payment flow.

Discovery:

- Events search page.
- Groups search page.
- Recommended events.
- Recommended groups.
- Location and topic filters.

### UI API Wiring Order

1. Group list/detail.
2. Create group.
3. Join/leave group.
4. Member management.
5. Group-scoped event creation.
6. RSVP with questions and guests.
7. Attendee check-in.
8. Recurring events.
9. Messaging.
10. Subscriptions and dues.
11. Analytics.

## Backward Compatibility Checklist

Before merging each phase:

- Existing `/api/v1/events` endpoints still work.
- Existing `CreateEventReq`, `UpdateEventReq`, `RSVPReq` clients still compile.
- Existing event rows with `group_id IS NULL` are visible exactly as before.
- Existing attendee rows with only `checked_in` still export correctly.
- Free event flow still works without Razorpay keys.
- Paid event flow still fails gracefully if Razorpay is not configured.
- Existing notification routing keys continue to work.
- Old clients that do not send RSVP answers can RSVP to events without required questions.
- Old clients that do not send guest count default to `0`.

## Test Strategy

### Unit Tests

- Slug generation.
- Time window validation.
- Recurrence generation.
- Permission calculation.
- RSVP guest limit validation.
- Required question validation.
- Attendance/check-in state changes.
- Group join/leave rules.
- Messaging visibility rules.
- Plan entitlement checks.

### Integration Tests

- Create group -> publish group -> join group -> create group event -> RSVP.
- Private group join request approval.
- Group member-only event visibility.
- Banned member cannot join or RSVP.
- Recurring series generation and one-occurrence cancellation.
- Event RSVP with required questions.
- Paid ticket RSVP and webhook confirmation.
- Group dues payment flow.
- Message thread send/read/mute/archive.

### Performance Tests

- Event listing with tags/location/date filters.
- Group listing with topics/location filters.
- Attendee export for large events.
- Member list for large groups.
- Recommendation endpoint under realistic data volume.

## Initial Implementation Milestones

### Milestone 1: Groups MVP

- Add group schema.
- Add group proto.
- Add group DB and service.
- Add gateway group routes.
- Add group auth guard.
- Add Docker Compose service and env config.
- UI: group list/detail/create/join.

### Milestone 2: Group Events

- Add nullable `events.group_id`.
- Add group-scoped create/list event APIs.
- Enforce group permissions.
- UI: create event inside group and show group events.

### Milestone 3: Advanced RSVP And Attendance

- Add guests, RSVP windows and event questions.
- Add attendance/check-in APIs.
- UI: RSVP form and attendee management.

### Milestone 4: Recurring Events

- Add event series schema and proto.
- Add occurrence generator.
- Add edit occurrence/edit series behavior.
- UI: recurrence editor.

### Milestone 5: Discovery

- Add saved events/groups.
- Add topic and location search.
- Add recommendation API.
- UI: discovery pages and filters.

### Milestone 6: Messaging

- Add thread/message schema.
- Add message service and routes.
- Add notification fanout.
- UI: inbox, discussions and attendee announcements.

### Milestone 7: Billing, Dues And Pro Tools

- Add plans/subscriptions/dues schema.
- Add entitlement checks.
- Add subscription and dues webhooks.
- UI: billing, dues, analytics and organizer tools.

## Notes

This is a large product expansion. The safest approach is to keep the current event module as the stable core, add groups as a new service, and then connect events to groups with nullable/additive fields. That gives us Meetup-like behavior without breaking standalone events or the current RSVP/payment implementation.
