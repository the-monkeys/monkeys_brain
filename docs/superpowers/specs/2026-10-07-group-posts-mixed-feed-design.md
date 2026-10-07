# Group Posts mixed feed + shared create menus — design

Date: 2026-10-07  
Status: **spec for review. Do not implement until this file is accepted.**  
Approach: **A** (client-side merge; no new feed API)  
Depends on: discussion audience (`public` | `group_only`) already shipped in engine + UI.

## Goal

Make group content feel like one product surface:

1. Group **Posts** tab shows blogs and discussions in one chronological feed.
2. Remove the separate **Discussions** tab.
3. Site **Create** menu: Discussion first, then Post, then Event, then Group.
4. Group header **Create event** becomes a create dropdown (Discussion / Post / Event).
5. Keep code DRY with shared modules reused by site header and group header.

## Non-goals

- New combined backend feed API
- Event audience / Public vs Members only for events (events stay as today)
- Changing left-nav site Discussions link
- Landing-page work
- Changing discussion or blog access rules (reuse existing audience + membership)

## Decisions (locked)

| Topic | Decision |
| --- | --- |
| Posts tab content | One mixed feed: blogs + discussions interleaved by `created_at` (newest first) |
| Discussions tab | Removed; `#discussions` resolves to Posts |
| Create Discussion (group header) | Stay on group, switch to Posts, open discussion composer |
| Audience | Blog + discussion: Public / Members only. Event: unchanged |
| Backend | No new list API; client fetches existing group blog list + group discussion list |

---

## 1. Group community tabs

### Behavior

- Tab order becomes: Posts, Events, About, Members, (+ Join requests / Invites for staff).
- Drop `discussions` from `GROUP_COMMUNITY_TABS`.
- `parseGroupCommunityTab`: treat `#discussions` (and aliases if any) as **Posts** so old links and bookmarks do not 404 or look empty.
- Default tab remains Posts (no hash).

### DRY

- Keep tab helpers in `groupCommunityTab.ts` as the single source of truth for labels/hash parsing.
- Update tests in `groupCommunityTab.test.ts` and `GroupCommunity.test.tsx` in the same change.

---

## 2. Mixed Posts feed

### Composition

Replace “Posts = blogs only, Discussions = discussions only” with one panel, e.g. `GroupPostsFeed`:

1. Reuse existing group blog fetch (`GroupBlogsPanel` data path or extracted hook).
2. Reuse existing group discussion list (`listDiscussions({ kind: 'group', slug })`).
3. Pure helper `mergeGroupFeedItems(blogs, discussions) → FeedItem[]` tagged `{ kind: 'blog' | 'discussion', createdAt, id, data }`.
4. Sort by `createdAt` descending; stable tie-break by `kind` then `id` so React keys stay stable.
5. Render with existing cards: blog card component(s) already used on the group, `DiscussionCard` for discussions. Do not fork card markup.

### Layout (edge case)

`GroupBlogsPanel` today uses a two-column grid; discussion cards are single-column list rows. A mixed feed must not alternate grid cells with list rows.

**v1:** one vertical column for the merged feed (full-width blog card + full-width discussion card). Prefer adapting the existing feed blog card to full width over inventing a second blog card. If the current group blog grid is required for blog-only polish, drop the grid only inside the mixed feed—do not keep two layouts in one list.

### Composer

- Mount existing `DiscussionComposer` at the top of Posts (same locks: login / join / member).
- `chooseAudience={group.visibility === 'public'}` unchanged.
- Optional: when group Create menu chooses Discussion, set local state `composerFocus` / `showComposer` and scroll composer into view after selecting Posts tab.
- After a successful discussion post, refresh **both** lists (or invalidate both queries) so the mixed feed updates.

### Empty / loading / error

- Loading: show while either list is still first-loading (avoid flashing “empty” then filling).
- Empty: only when both lists succeeded and both are empty. Copy covers both content types (update `groupFeedEmptyCopy` / related helper rather than hardcoding in JSX).
- Partial error: if one source fails, show the other list plus a non-blocking error line for the failed source. Do not blank the whole tab.

### Pagination (edge case)

There is no shared cursor across blogs and discussions.

**v1 rule:** fetch the first page of each (existing page sizes), merge that window only. “Load more” (if blogs already have it) loads the next blog page and re-merges with the discussions already held; discussions get the same pattern if/when they support cursor load-more.

Document in UI only if we add an explicit control; do not invent a fake global cursor. A true interleaved infinite feed is out of scope (would need Approach B).

### Visibility (edge cases)

| Viewer | Blogs in feed | Discussions in feed | Composer |
| --- | --- | --- | --- |
| Active member | Per existing group blog rules (incl. members-only) | All visible group discussions for members | Allowed |
| Non-member on public group | Public blogs only | Public discussions only (`audience=public`) | Join / login lock |
| Logged-out | Same as non-member for public group | Same | Login lock |
| Private / unlisted group, non-member | Panel already hidden | N/A | N/A |

Do not re-implement ACL in the merge helper; trust list APIs.

### Identity / keys

- Feed item key: `blog:${blogId}` or `discussion:${publicId}` so kinds never collide.
- Do not use array index as key.

---

## 3. Shared create menu module

### Why

Site header `CreateButton` and group header create control must stay in sync for labels, order, and icons. One module owns the item list; thin wrappers supply `variant` and navigation handlers.

### API (sketch)

```ts
type CreateMenuVariant = 'site' | 'group';

type CreateMenuItemId = 'discussion' | 'post' | 'event' | 'group';

// site order: discussion, post, event, group
// group order: discussion, post, event  (no "create group")
```

`CreateMenu` (or `CreateMenuContent`) renders `DropdownMenuItem`s from `createMenuItems(variant)`.

Handlers injected by parent:

| Item | Site | Group |
| --- | --- | --- |
| Discussion | Navigate to `/discussions` (site composer) | `onCreateDiscussion()` → Posts tab + focus composer |
| Post | `CREATE_ROUTE` | Same create-post entry the product already uses for group blogs (preserve current query/body `group_slug` wiring; do not invent a second editor). If none exists in header today, link `CREATE_ROUTE` with the established group-scoped create params used elsewhere in the app. |
| Event | `CREATE_EVENT_ROUTE` | ` /groups/:slug/events/new` (unchanged) |
| Group | `CREATE_GROUP_ROUTE` | Omitted |

### Who sees the group header menu

- Keep the control where **Create event** is today (organizers / `canManageGroup`), unless product already exposes create-post to members elsewhere—do not silently broaden event creation.
- Members who are not managers still create discussions via the Posts-tab composer (existing). They do not need the header dropdown for Discussion to remain usable.

### Audience

- Not chosen in the dropdown.
- Discussion: Public / Members only live on `DiscussionComposer` after opening.
- Post: existing blog audience UI on the create-post flow.
- Event: no audience UI (as today).

### DRY checklist

- Icons + labels defined once in the create-menu module.
- `CreateButton` becomes a thin trigger + `CreateMenu variant="site"`.
- Group header uses the same menu with `variant="group"` and group-scoped handlers.
- No copy-paste of three Link rows in two files.

---

## 4. Cleanup

- Stop mounting `GroupDiscussions` as a tab panel; fold its list + composer responsibilities into `GroupPostsFeed` (or rename/repurpose `GroupDiscussions` to avoid a dead export).
- Prefer **one** group posts entry component rather than keeping both `GroupBlogsPanel` and `GroupDiscussions` as parallel tab roots. Extract shared fetch/refresh hooks if both panels currently duplicate auth/locked patterns.
- Update empty-copy and tab tests; delete or rewrite Discussion-tab-only assertions.

---

## 5. Testing

### Unit

- `mergeGroupFeedItems`: order, stable ties, empty inputs, mixed kinds, key prefix uniqueness.
- `createMenuItems('site' | 'group')`: order and membership of items.
- `parseGroupCommunityTab('#discussions')` → `posts`.
- `groupCommunityTabs` no longer includes `discussions`.

### Component

- Group Posts shows a discussion card and a blog card in time order (mocked lists).
- Group Create Discussion calls into Posts + composer focus (mock handler).
- Site Create lists Discussion before Post.

### Manual

- Public group as member: post Public vs Members only discussion; both appear on Posts; only Public appears on site Discussions feed.
- Non-member: sees public items only; join-to-post / join-to-reply unchanged.
- `#discussions` opens Posts.
- 390px: create menus and mixed feed readable; no horizontal overflow on cards.

---

## 6. File touch map (expected)

Frontend (`local/the_monkeys`):

- `src/lib/groupCommunityTab.ts` (+ tests)
- `src/lib/mergeGroupFeedItems.ts` (new, + tests)
- `src/components/create/CreateMenu.tsx` (or under `buttons/`, + tests)
- `src/components/buttons/createButton.tsx`
- `src/app/groups/[slug]/GroupDetailClient.tsx`
- `src/components/groups/detail/GroupCommunity.tsx`
- `src/components/groups/detail/GroupBlogsPanel.tsx` and/or new `GroupPostsFeed.tsx`
- `src/components/discussions/GroupDiscussions.tsx` (fold or thin)
- Related `__tests__`

Backend: **none required** for this UX pass.

---

## 7. Risks

| Risk | Mitigation |
| --- | --- |
| Merge window not globally chronological beyond first pages | Accept for v1; document; revisit only if product requires infinite interleaved scroll |
| Double-fetch on every Posts visit | Parallel `Promise.all` / react-query; no waterfall |
| Duplicate UI for create menus | Single `CreateMenu` module |
| Stale `#discussions` links | Alias to Posts in parser |

---

## Spec self-review

- No TBD/placeholder sections left for core behavior.
- Pagination limitation is explicit (v1 window merge, not fake global cursor).
- Layout conflict (blog grid vs discussion list) resolved: single column in mixed feed.
- Create Post group URL defers to existing group-scoped create wiring rather than inventing a new editor.
- Backend unchanged; ACL stays in list APIs.
- Scope matches Approach A only.

## Approval

Please confirm this spec (or list edits). After acceptance, implementation follows via a written plan (TDD, DRY modules first).
