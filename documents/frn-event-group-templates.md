# Freerange templates: events and groups

Copy each block into Freerange: **Templates → Create**. Use the **Name** exactly. Channel is one of `in_app`, `sse`, or `email`.

App: The Monkeys (your existing API key). Locale: `en`.

Placeholders use Go template form: `{{.variable_name}}`.

In-app and SSE use the same words. Email bodies are HTML so Gmail can link
`@username` and titles. The notification service always sends these extra
variables on every channel:

- `actor_url`, `follower_url`, `liker_url`, `inviter_url`, `coauthor_url`, `remover_url`, `publisher_url`, `commenter_url`
- `event_url`, `group_url`, `blog_url`
- `home_url`, `settings_url`, `events_home_url`, `groups_home_url`, `notifications_url`

If a name already exists, skip that block. For email templates that already
exist, edit the body to the HTML version below.

---

## How to paste

1. Name
2. Channel
3. Subject (title in the bell)
4. Body (gray line under the title)
5. Variables (comma-separated in Freerange if it asks)

---

## Events: application received

Host-review is on. A guest applied. Send to organizer and co-hosts. In-app, SSE, and email.

### event_application_received_inapp

- Channel: `in_app`
- Category: `events`
- Priority: `high`
- Subject: `New application`
- Body: `{{.actor_name}} applied to join "{{.event_title}}".`
- Variables: `actor_name`, `event_title`, `event_slug`

### event_application_received_sse

- Channel: `sse`
- Category: `events`
- Priority: `high`
- Subject: `New application`
- Body: `{{.actor_name}} applied to join "{{.event_title}}".`
- Variables: `actor_name`, `event_title`, `event_slug`

### event_application_received_email

- Channel: `email`
- Category: `events`
- Priority: `high`
- Subject: `New application for {{.event_title}}`
- Body: `<a href="{{.actor_url}}">@{{.actor_name}}</a> applied to join "<a href="{{.event_url}}">{{.event_title}}</a>". Open the event to approve or reject.`
- Variables: `actor_name`, `actor_url`, `event_title`, `event_slug`, `event_url`

---

## Events: application approved

Send to the guest. In-app, SSE, and email.

`next_step` is set by the backend:

- Paid meetup: `Pay to confirm your seat.`
- Free meetup: `You are in.`

### event_application_approved_inapp

- Channel: `in_app`
- Category: `events`
- Priority: `high`
- Subject: `Application approved`
- Body: `{{.actor_name}} approved your application for "{{.event_title}}". {{.next_step}}`
- Variables: `actor_name`, `event_title`, `event_slug`, `next_step`

### event_application_approved_sse

- Channel: `sse`
- Category: `events`
- Priority: `high`
- Subject: `Application approved`
- Body: `{{.actor_name}} approved your application for "{{.event_title}}". {{.next_step}}`
- Variables: `actor_name`, `event_title`, `event_slug`, `next_step`

### event_application_approved_email

- Channel: `email`
- Category: `events`
- Priority: `high`
- Subject: `You were approved for {{.event_title}}`
- Body: `<a href="{{.actor_url}}">@{{.actor_name}}</a> approved your application for "<a href="{{.event_url}}">{{.event_title}}</a>". {{.next_step}} Open the event to continue.`
- Variables: `actor_name`, `actor_url`, `event_title`, `event_slug`, `event_url`, `next_step`

---

## Events: application rejected

Send to the guest. In-app, SSE, and email. `reason` may be empty.

### event_application_rejected_inapp

- Channel: `in_app`
- Category: `events`
- Priority: `high`
- Subject: `Application not approved`
- Body: `{{.actor_name}} did not approve your application for "{{.event_title}}".`
- Variables: `actor_name`, `event_title`, `event_slug`, `reason`

### event_application_rejected_sse

- Channel: `sse`
- Category: `events`
- Priority: `high`
- Subject: `Application not approved`
- Body: `{{.actor_name}} did not approve your application for "{{.event_title}}".`
- Variables: `actor_name`, `event_title`, `event_slug`, `reason`

### event_application_rejected_email

- Channel: `email`
- Category: `events`
- Priority: `high`
- Subject: `Your application for {{.event_title}} was not approved`
- Body: `<a href="{{.actor_url}}">@{{.actor_name}}</a> did not approve your application for "<a href="{{.event_url}}">{{.event_title}}</a>". You can apply to other <a href="{{.events_home_url}}">meetups</a>.`
- Variables: `actor_name`, `actor_url`, `event_title`, `event_slug`, `event_url`, `reason`, `events_home_url`

---

## Events: open RSVP (host-review off)

Someone is confirmed or waiting to pay. Send to organizer and co-hosts. In-app and SSE only. No email.

### event_rsvp_host_notice_inapp

- Channel: `in_app`
- Category: `events`
- Priority: `normal`
- Subject: `New RSVP`
- Body: `{{.actor_name}} RSVP'd to "{{.event_title}}".`
- Variables: `actor_name`, `event_title`, `event_slug`

### event_rsvp_host_notice_sse

- Channel: `sse`
- Category: `events`
- Priority: `normal`
- Subject: `New RSVP`
- Body: `{{.actor_name}} RSVP'd to "{{.event_title}}".`
- Variables: `actor_name`, `event_title`, `event_slug`

---

## Events: plan-breaking update

Time, timezone, location, meeting link, cancellation, or ticket price changed. Send to people who are going or still paying. In-app, SSE, and email.

### event_updated_inapp

- Channel: `in_app`
- Category: `events`
- Priority: `high`
- Subject: `Event updated`
- Body: `"{{.event_title}}" changed. {{.change_summary}}`
- Variables: `event_title`, `event_slug`, `change_summary`

### event_updated_sse

- Channel: `sse`
- Category: `events`
- Priority: `high`
- Subject: `Event updated`
- Body: `"{{.event_title}}" changed. {{.change_summary}}`
- Variables: `event_title`, `event_slug`, `change_summary`

### event_updated_email

- Channel: `email`
- Category: `events`
- Priority: `high`
- Subject: `{{.event_title}} was updated`
- Body: `"<a href="{{.event_url}}">{{.event_title}}</a>" changed. {{.change_summary}} Open the event to see the new details.`
- Variables: `event_title`, `event_slug`, `event_url`, `change_summary`

---

## Groups: join request (private or unlisted)

Send to organizer and co-admins. In-app, SSE, and email.

### group_join_requested_inapp

- Channel: `in_app`
- Category: `events`
- Priority: `high`
- Subject: `Join request`
- Body: `{{.actor_name}} asked to join "{{.group_name}}".`
- Variables: `actor_name`, `group_name`, `group_slug`

### group_join_requested_sse

- Channel: `sse`
- Category: `events`
- Priority: `high`
- Subject: `Join request`
- Body: `{{.actor_name}} asked to join "{{.group_name}}".`
- Variables: `actor_name`, `group_name`, `group_slug`

### group_join_requested_email

- Channel: `email`
- Category: `events`
- Priority: `high`
- Subject: `Join request for {{.group_name}}`
- Body: `<a href="{{.actor_url}}">@{{.actor_name}}</a> asked to join "<a href="{{.group_url}}">{{.group_name}}</a>". Open the group to approve or reject.`
- Variables: `actor_name`, `actor_url`, `group_name`, `group_slug`, `group_url`

---

## Groups: join approved

Send to the person. In-app, SSE, and email.

### group_join_approved_inapp

- Channel: `in_app`
- Category: `events`
- Priority: `high`
- Subject: `You joined the group`
- Body: `{{.actor_name}} approved your request to join "{{.group_name}}".`
- Variables: `actor_name`, `group_name`, `group_slug`

### group_join_approved_sse

- Channel: `sse`
- Category: `events`
- Priority: `high`
- Subject: `You joined the group`
- Body: `{{.actor_name}} approved your request to join "{{.group_name}}".`
- Variables: `actor_name`, `group_name`, `group_slug`

### group_join_approved_email

- Channel: `email`
- Category: `events`
- Priority: `high`
- Subject: `You can join {{.group_name}}`
- Body: `<a href="{{.actor_url}}">@{{.actor_name}}</a> approved your request to join "<a href="{{.group_url}}">{{.group_name}}</a>". Open the group to say hello.`
- Variables: `actor_name`, `actor_url`, `group_name`, `group_slug`, `group_url`

---

## Groups: join rejected

Send to the person. In-app, SSE, and email.

### group_join_rejected_inapp

- Channel: `in_app`
- Category: `events`
- Priority: `high`
- Subject: `Join request not approved`
- Body: `{{.actor_name}} did not approve your request to join "{{.group_name}}".`
- Variables: `actor_name`, `group_name`, `group_slug`

### group_join_rejected_sse

- Channel: `sse`
- Category: `events`
- Priority: `high`
- Subject: `Join request not approved`
- Body: `{{.actor_name}} did not approve your request to join "{{.group_name}}".`
- Variables: `actor_name`, `group_name`, `group_slug`

### group_join_rejected_email

- Channel: `email`
- Category: `events`
- Priority: `high`
- Subject: `Your request to join {{.group_name}} was not approved`
- Body: `<a href="{{.actor_url}}">@{{.actor_name}}</a> did not approve your request to join "<a href="{{.group_url}}">{{.group_name}}</a>". You can join other public <a href="{{.groups_home_url}}">groups</a>.`
- Variables: `actor_name`, `actor_url`, `group_name`, `group_slug`, `group_url`, `groups_home_url`

---

## Groups: public join

Someone joined a public group. Send to organizer and co-admins. In-app and SSE only. No email.

### group_member_joined_inapp

- Channel: `in_app`
- Category: `events`
- Priority: `normal`
- Subject: `New member`
- Body: `{{.actor_name}} joined "{{.group_name}}".`
- Variables: `actor_name`, `group_name`, `group_slug`

### group_member_joined_sse

- Channel: `sse`
- Category: `events`
- Priority: `normal`
- Subject: `New member`
- Body: `{{.actor_name}} joined "{{.group_name}}".`
- Variables: `actor_name`, `group_name`, `group_slug`

---

## Groups: new event in the group

Send to every active member. In-app and SSE only. No email.

### group_event_published_inapp

- Channel: `in_app`
- Category: `events`
- Priority: `normal`
- Subject: `New group event`
- Body: `New event in "{{.group_name}}": "{{.event_title}}".`
- Variables: `actor_name`, `group_name`, `group_slug`, `event_title`, `event_slug`

### group_event_published_sse

- Channel: `sse`
- Category: `events`
- Priority: `normal`
- Subject: `New group event`
- Body: `New event in "{{.group_name}}": "{{.event_title}}".`
- Variables: `actor_name`, `group_name`, `group_slug`, `event_title`, `event_slug`

---

## Do not create

- No template for “application was viewed”.
- Do not recreate likes, follows, login, RSVP confirmed, waitlist, reminder, cancel, or refund if they already exist in Freerange.

## Checklist (27 new templates)

1. event_application_received_inapp / _sse / _email
2. event_application_approved_inapp / _sse / _email
3. event_application_rejected_inapp / _sse / _email
4. event_rsvp_host_notice_inapp / _sse
5. event_updated_inapp / _sse / _email
6. group_join_requested_inapp / _sse / _email
7. group_join_approved_inapp / _sse / _email
8. group_join_rejected_inapp / _sse / _email
9. group_member_joined_inapp / _sse
10. group_event_published_inapp / _sse
