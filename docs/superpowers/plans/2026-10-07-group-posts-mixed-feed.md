# Group Posts Mixed Feed + Shared Create Menus Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Put blogs and discussions in one chronological group Posts feed, remove the Discussions tab, and share one Create menu module for the site header and group header.

**Architecture:** Frontend-only. Pure helpers merge and menu item lists; thin React wrappers wire navigation and composer focus. Fetch group blogs via existing `useGroupBlogs` and group discussions via existing `listDiscussions`; render existing `FeedBlogCard` and `DiscussionCard` in one column. No new backend feed API.

**Tech Stack:** Next.js 14 App Router, React, Vitest + Testing Library, existing `@the-monkeys/ui` dropdowns, TanStack Query (`useGroupBlogs`).

**Spec:** `docs/superpowers/specs/2026-10-07-group-posts-mixed-feed-design.md` (Approach A).

## Global Constraints

- Frontend work lives under `local/the_monkeys/apps/the_monkeys/` (git root `local/the_monkeys`, branch `feature/discussions`).
- Engine repo needs no API changes for this plan; only the plan/spec docs may land there.
- TDD: failing test first, then minimal code.
- DRY: one `createMenuItems` source, one `mergeGroupFeedItems` helper, one Posts panel (no parallel Discussions tab).
- Do not stage/commit landing-page files or empty `a`.
- No em dashes in UI copy.
- Mobile-first; verify 390px manually at the end.
- Event create stays as today (no Public / Members only).
- Blog + discussion audience stays Public / Members only on existing composers/editors.
- PowerShell: do not use `&&` in shell commands.

## File structure

| File | Responsibility |
| --- | --- |
| `src/lib/mergeGroupFeedItems.ts` | Pure merge + sort of blogs and discussions |
| `src/lib/createMenuItems.ts` | Pure site/group create menu item descriptors |
| `src/components/create/CreateMenu.tsx` | Shared dropdown content from descriptors + handlers |
| `src/components/buttons/createButton.tsx` | Site Create trigger; uses CreateMenu |
| `src/components/groups/detail/GroupCreateMenu.tsx` | Group header Create trigger; uses CreateMenu |
| `src/components/groups/detail/GroupPostsFeed.tsx` | Composer + mixed feed (replaces tab split) |
| `src/lib/groupCommunityTab.ts` | Drop discussions tab; alias `#discussions` → posts |
| `src/lib/groupPerms.ts` | Empty copy mentions discussions for posts |
| `src/components/groups/detail/GroupCommunity.tsx` | Posts → GroupPostsFeed; remove discussions tab branch |
| `src/app/groups/[slug]/GroupDetailClient.tsx` | GroupCreateMenu + discussion-open token |
| `src/components/discussions/GroupDiscussions.tsx` | Delete or re-export thin alias only if tests still import; prefer delete after fold |

Paths below are relative to `local/the_monkeys/apps/the_monkeys/` unless noted.

---

### Task 1: `mergeGroupFeedItems` helper

**Files:**
- Create: `src/lib/mergeGroupFeedItems.ts`
- Test: `__tests__/src/lib/mergeGroupFeedItems.test.ts`

**Interfaces:**
- Consumes: `Blog` from `@/services/blog/blogTypes` (`blog_id`, `published_time`); `Discussion` from `@/services/discussions/discussionsTypes` (`public_id`, `created_at`); `discussionInstantMs` from `@/lib/discussionTime`
- Produces:

```ts
export type GroupFeedItem =
  | { kind: 'blog'; id: string; createdAtMs: number; blog: Blog }
  | { kind: 'discussion'; id: string; createdAtMs: number; discussion: Discussion };

export function mergeGroupFeedItems(
  blogs: Blog[],
  discussions: Discussion[]
): GroupFeedItem[];
```

- [ ] **Step 1: Write the failing test**

```ts
import { mergeGroupFeedItems } from '@/lib/mergeGroupFeedItems';
import { describe, expect, it } from 'vitest';

describe('mergeGroupFeedItems', () => {
  it('interleaves by time newest first and uses stable prefixed ids', () => {
    const blogs = [
      { blog_id: 'b1', published_time: '2026-10-07T12:00:00.000Z' },
      { blog_id: 'b2', published_time: '2026-10-07T10:00:00.000Z' },
    ] as never[];
    const discussions = [
      { public_id: 'd1', body: 'hi', status: 'visible', created_at: '2026-10-07T11:00:00.000Z' },
    ] as never[];

    const items = mergeGroupFeedItems(blogs, discussions);
    expect(items.map((i) => `${i.kind}:${i.id}`)).toEqual([
      'blog:b1',
      'discussion:d1',
      'blog:b2',
    ]);
  });

  it('returns empty when both inputs are empty', () => {
    expect(mergeGroupFeedItems([], [])).toEqual([]);
  });

  it('ties break by kind then id for stable order', () => {
    const t = '2026-10-07T12:00:00.000Z';
    const items = mergeGroupFeedItems(
      [{ blog_id: 'b', published_time: t } as never],
      [{ public_id: 'd', body: 'x', status: 'visible', created_at: t } as never]
    );
    expect(items.map((i) => i.kind)).toEqual(['blog', 'discussion']);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run (cwd `apps/the_monkeys`):

```bash
npx vitest run __tests__/src/lib/mergeGroupFeedItems.test.ts
```

Expected: FAIL (module not found or export missing)

- [ ] **Step 3: Write minimal implementation**

```ts
import { discussionInstantMs } from '@/lib/discussionTime';
import type { Blog } from '@/services/blog/blogTypes';
import type { Discussion } from '@/services/discussions/discussionsTypes';

export type GroupFeedItem =
  | { kind: 'blog'; id: string; createdAtMs: number; blog: Blog }
  | {
      kind: 'discussion';
      id: string;
      createdAtMs: number;
      discussion: Discussion;
    };

function blogMs(blog: Blog): number {
  const ms = Date.parse(blog.published_time);
  return Number.isFinite(ms) ? ms : 0;
}

export function mergeGroupFeedItems(
  blogs: Blog[],
  discussions: Discussion[]
): GroupFeedItem[] {
  const items: GroupFeedItem[] = [
    ...blogs.map((blog) => ({
      kind: 'blog' as const,
      id: blog.blog_id,
      createdAtMs: blogMs(blog),
      blog,
    })),
    ...discussions.map((discussion) => {
      const ms = discussionInstantMs(discussion.created_at);
      return {
        kind: 'discussion' as const,
        id: discussion.public_id,
        createdAtMs: Number.isFinite(ms) ? ms : 0,
        discussion,
      };
    }),
  ];
  return items.sort((a, b) => {
    if (b.createdAtMs !== a.createdAtMs) return b.createdAtMs - a.createdAtMs;
    if (a.kind !== b.kind) return a.kind < b.kind ? -1 : 1;
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
  });
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
npx vitest run __tests__/src/lib/mergeGroupFeedItems.test.ts
```

Expected: PASS

- [ ] **Step 5: Commit** (frontend repo)

```bash
git add apps/the_monkeys/src/lib/mergeGroupFeedItems.ts apps/the_monkeys/__tests__/src/lib/mergeGroupFeedItems.test.ts
git commit -m "feat: add mergeGroupFeedItems for group Posts feed"
```

---

### Task 2: `createMenuItems` helper

**Files:**
- Create: `src/lib/createMenuItems.ts`
- Test: `__tests__/src/lib/createMenuItems.test.ts`

**Interfaces:**
- Produces:

```ts
export type CreateMenuVariant = 'site' | 'group';
export type CreateMenuItemId = 'discussion' | 'post' | 'event' | 'group';

export type CreateMenuItem = {
  id: CreateMenuItemId;
  label: string;
  icon: IconName; // from @/components/icon
};

export function createMenuItems(variant: CreateMenuVariant): CreateMenuItem[];
```

Site order: discussion, post, event, group.  
Group order: discussion, post, event.

Labels: `Create Discussion`, `Create Post`, `Create Event`, `Create Group`.  
Icons (match current CreateButton): discussion `RiChat1` (or `RiDiscuss` if present—prefer an icon already used in nav for Discussions), post `RiDraft`, event `RiCalendar`, group `RiGroup`.

- [ ] **Step 1: Write the failing test**

```ts
import { createMenuItems } from '@/lib/createMenuItems';
import { describe, expect, it } from 'vitest';

describe('createMenuItems', () => {
  it('lists discussion before post on site and omits create group on group', () => {
    expect(createMenuItems('site').map((i) => i.id)).toEqual([
      'discussion',
      'post',
      'event',
      'group',
    ]);
    expect(createMenuItems('group').map((i) => i.id)).toEqual([
      'discussion',
      'post',
      'event',
    ]);
  });

  it('uses Create Discussion as the first label', () => {
    expect(createMenuItems('site')[0].label).toBe('Create Discussion');
  });
});
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
npx vitest run __tests__/src/lib/createMenuItems.test.ts
```

- [ ] **Step 3: Implement `createMenuItems`**

Use the icon names above; if `RiChat1` is not in `IconName`, use the same icon as the left-nav Discussions item in `routeConstants` / nav config.

- [ ] **Step 4: Run test — expect PASS**

- [ ] **Step 5: Commit**

```bash
git add apps/the_monkeys/src/lib/createMenuItems.ts apps/the_monkeys/__tests__/src/lib/createMenuItems.test.ts
git commit -m "feat: add shared createMenuItems descriptors"
```

---

### Task 3: Shared `CreateMenu` + site `CreateButton`

**Files:**
- Create: `src/components/create/CreateMenu.tsx`
- Modify: `src/components/buttons/createButton.tsx`
- Test: `__tests__/src/components/create/CreateMenu.test.tsx`

**Interfaces:**
- Consumes: `createMenuItems`, `CreateMenuVariant`, `CreateMenuItemId`
- Produces:

```tsx
export function CreateMenuContent({
  variant,
  onSelect,
}: {
  variant: CreateMenuVariant;
  onSelect: (id: CreateMenuItemId) => void;
}): JSX.Element;
```

`CreateButton` keeps its trigger chrome; content calls `onSelect` which navigates:

| id | href |
| --- | --- |
| discussion | `DISCUSSIONS_ROUTE` |
| post | `CREATE_ROUTE` |
| event | `CREATE_EVENT_ROUTE` |
| group | `CREATE_GROUP_ROUTE` |

Prefer `router.push` or `Link` via mapping in CreateButton so CreateMenu stays link-agnostic (`onSelect` only). Separators: optional thin separator after post on site (match current visual between post block and event/group if useful; YAGNI if one continuous list is fine—keep a single list without separator unless design needs it).

- [ ] **Step 1: Failing test — site order in DOM**

```tsx
import { CreateMenuContent } from '@/components/create/CreateMenu';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

describe('CreateMenuContent', () => {
  it('fires discussion before post for site variant', async () => {
    const onSelect = vi.fn();
    render(<CreateMenuContent variant='site' onSelect={onSelect} />);
    const items = screen.getAllByRole('menuitem');
    expect(items.map((el) => el.textContent)).toEqual([
      'Create Discussion',
      'Create Post',
      'Create Event',
      'Create Group',
    ]);
    await userEvent.click(items[0]);
    expect(onSelect).toHaveBeenCalledWith('discussion');
  });
});
```

Wrap with the same Dropdown primitives if menuitem roles require DropdownMenu root—use the pattern other dropdown tests in the repo use, or render items as buttons with `role="menuitem"` for unit isolation.

- [ ] **Step 2: Run — expect FAIL**

- [ ] **Step 3: Implement CreateMenuContent + refactor CreateButton**

CreateButton:

```tsx
const router = useRouter();
// ...
<CreateMenuContent
  variant='site'
  onSelect={(id) => {
    const href = { discussion: DISCUSSIONS_ROUTE, post: CREATE_ROUTE, event: CREATE_EVENT_ROUTE, group: CREATE_GROUP_ROUTE }[id];
    router.push(href);
  }}
/>
```

Or map to `Link` inside CreateMenuContent via optional `hrefFor(id)` prop—pick one style and stick to it. Prefer `hrefFor` + Link for accessibility prefetch:

```tsx
export function CreateMenuContent({
  variant,
  hrefFor,
  onSelect,
}: {
  variant: CreateMenuVariant;
  hrefFor?: (id: CreateMenuItemId) => string | undefined;
  onSelect?: (id: CreateMenuItemId) => void;
})
```

Site: provide `hrefFor`. Group: provide `onSelect` only for discussion; `hrefFor` for post/event.

- [ ] **Step 4: Run CreateMenu test + smoke CreateButton still renders**

```bash
npx vitest run __tests__/src/components/create/CreateMenu.test.tsx
```

- [ ] **Step 5: Commit**

```bash
git commit -m "feat: share CreateMenu for site Create button"
```

---

### Task 4: Drop Discussions tab; alias `#discussions` → posts

**Files:**
- Modify: `src/lib/groupCommunityTab.ts`
- Modify: `__tests__/src/lib/groupCommunityTab.test.ts`
- Modify: `__tests__/src/components/groups/detail/GroupCommunity.test.tsx`
- Modify: `src/components/groups/detail/GroupCommunity.tsx` (tab labels / branch; Posts still GroupBlogsPanel until Task 5)

**Interfaces:**
- `GROUP_COMMUNITY_TABS` without `'discussions'`
- `TAB_ALIASES`: `{ blogs: 'posts', discussions: 'posts' }`
- Remove `discussions` from `TAB_LABELS` in GroupCommunity

- [ ] **Step 1: Update tab unit tests to the new contract (they will fail against old code)**

Assert:

```ts
expect(groupCommunityTabs(false)).toEqual([
  'posts',
  'events',
  'about',
  'members',
]);
expect(parseGroupCommunityTab('#discussions', false)).toBe('posts');
expect(groupCommunityHash('discussions' as never)); // remove hash test for discussions
```

GroupCommunity test tab list without Discussions; remove any test that opens Discussions tab (read rest of file and rewrite).

- [ ] **Step 2: Run tests — expect FAIL**

```bash
npx vitest run __tests__/src/lib/groupCommunityTab.test.ts __tests__/src/components/groups/detail/GroupCommunity.test.tsx
```

- [ ] **Step 3: Implement tab list + alias + GroupCommunity branch removal**

In GroupCommunity, delete `discussions` label and `tab === 'discussions'` branch. Temporarily keep Posts → `GroupBlogsPanel` only.

- [ ] **Step 4: Run tests — expect PASS**

- [ ] **Step 5: Commit**

```bash
git commit -m "feat: remove group Discussions tab; alias hash to Posts"
```

---

### Task 5: `GroupPostsFeed` mixed feed + composer

**Files:**
- Create: `src/components/groups/detail/GroupPostsFeed.tsx`
- Modify: `src/components/groups/detail/GroupCommunity.tsx` (Posts → `GroupPostsFeed`)
- Modify: `src/lib/groupPerms.ts` empty copy for posts
- Modify: `__tests__/src/lib/groupFeedEmptyCopy.test.ts`
- Create: `__tests__/src/components/groups/detail/GroupPostsFeed.test.tsx`
- Modify or delete: `src/components/discussions/GroupDiscussions.tsx` and its tests (fold into GroupPostsFeed tests)

**Interfaces:**
- Consumes: `useGroupBlogs`, `listDiscussions`, `mergeGroupFeedItems`, `DiscussionComposer`, `DiscussionFeed` or map `DiscussionCard`, `FeedBlogCard` + `fromBlog`, `groupFeedEmptyCopy`
- Produces:

```tsx
export function GroupPostsFeed({
  group,
  focusComposerToken = 0,
}: {
  group: GroupItem;
  focusComposerToken?: number;
}): JSX.Element;
```

Behavior:

1. Load blogs via `useGroupBlogs(group.slug)`.
2. Load discussions via `listDiscussions` (same pattern as current `GroupDiscussions`).
3. Wait for both first loads before empty state.
4. Merge with `mergeGroupFeedItems`.
5. Single column: for each item, blog → `FeedBlogCard`; discussion → same row component `DiscussionFeed` uses (prefer mapping to existing card; if `DiscussionFeed` only accepts a list, either pass discussion-only slices in order **or** render `DiscussionCard` per item—do not put blogs inside DiscussionFeed).
6. Composer on top with existing locks + `chooseAudience={group.visibility === 'public'}`.
7. `useEffect` on `focusComposerToken`: when token > 0, `composerRef.current?.scrollIntoView` and focus textarea if possible.
8. Partial error: show feed from the successful source + `role="alert"` line for the failed one.
9. Blog “Load more” keeps working; re-merge with current discussions after fetch.
10. Empty copy: update member posts string to `No posts or discussions yet.` (or equivalent without em dash).

- [ ] **Step 1: Failing GroupPostsFeed test**

Mock `useGroupBlogs` and `listDiscussions`. Assert order of visible titles/bodies and that composer radios exist for public group when member.

```tsx
// Sketch assertions
expect(screen.getByText('newer blog title or body')).toBeTruthy();
expect(screen.getByText('discussion body')).toBeTruthy();
// DOM order: discussion between blogs if times dictate
```

Update empty-copy test expected string.

- [ ] **Step 2: Run — expect FAIL**

- [ ] **Step 3: Implement GroupPostsFeed; wire GroupCommunity; update empty copy; retire GroupDiscussions tab usage**

If `GroupDiscussions.tsx` is unused, delete it and update `GroupDiscussions.test.tsx` into `GroupPostsFeed.test.tsx` (composer + list behaviors).

- [ ] **Step 4: Run**

```bash
npx vitest run __tests__/src/components/groups/detail/GroupPostsFeed.test.tsx __tests__/src/lib/groupFeedEmptyCopy.test.ts __tests__/src/components/groups/detail/GroupCommunity.test.tsx __tests__/src/components/discussions/GroupDiscussions.test.tsx
```

Fix any leftover imports. Expected: PASS (delete obsolete GroupDiscussions tests if file removed).

- [ ] **Step 5: Commit**

```bash
git commit -m "feat: mix blogs and discussions on group Posts tab"
```

---

### Task 6: Group header `GroupCreateMenu` + composer focus token

**Files:**
- Create: `src/components/groups/detail/GroupCreateMenu.tsx`
- Modify: `src/app/groups/[slug]/GroupDetailClient.tsx`
- Modify: `src/components/groups/detail/GroupCommunity.tsx` (accept `focusDiscussionToken`, pass through; expose `onRequestPostsTab` **or** lift tab selection)

**Recommended wiring (keep DRY, avoid ref soup):**

```tsx
// GroupDetailClient
const [focusDiscussionToken, setFocusDiscussionToken] = useState(0);
const [postsTabToken, setPostsTabToken] = useState(0);

{manage && (
  <GroupCreateMenu
    slug={group.slug}
    onCreateDiscussion={() => {
      setPostsTabToken((n) => n + 1);
      setFocusDiscussionToken((n) => n + 1);
    }}
  />
)}
<GroupCommunity
  group={group}
  forcePostsToken={postsTabToken}
  focusDiscussionToken={focusDiscussionToken}
/>
```

`GroupCommunity`: `useEffect` on `forcePostsToken` → `selectTab('posts')`. Pass `focusDiscussionToken` to `GroupPostsFeed`.

`GroupCreateMenu` handlers:

| id | action |
| --- | --- |
| discussion | `onCreateDiscussion()` |
| post | `router.push(CREATE_ROUTE)` (publish UI already supports choosing this group) |
| event | `router.push(\`${GROUPS_ROUTE}/${slug}/events/new\`)` |

Trigger label: `Create` with chevron (or `Create` matching site), `variant='outline'` to replace the old outline “Create event” button. Still only when `canManageGroup` / `manage` as today.

- [ ] **Step 1: Failing test for GroupCreateMenu item ids/order**

```tsx
expect(screen.getAllByRole('menuitem').map((el) => el.textContent)).toEqual([
  'Create Discussion',
  'Create Post',
  'Create Event',
]);
```

- [ ] **Step 2: Run — FAIL**

- [ ] **Step 3: Implement menu + GroupDetailClient + Community tokens**

- [ ] **Step 4: Run GroupCreateMenu + GroupCommunity + GroupPostsFeed tests**

- [ ] **Step 5: Commit**

```bash
git commit -m "feat: group header Create menu opens discussion composer on Posts"
```

---

### Task 7: Verification

**Files:** none new

- [ ] **Step 1: Run full discussion/group related vitest**

```bash
npx vitest run __tests__/src/lib/mergeGroupFeedItems.test.ts __tests__/src/lib/createMenuItems.test.ts __tests__/src/lib/groupCommunityTab.test.ts __tests__/src/lib/groupFeedEmptyCopy.test.ts __tests__/src/components/create __tests__/src/components/groups/detail __tests__/src/components/discussions
```

Expected: all PASS

- [ ] **Step 2: Lint touched files**

```bash
npx eslint --fix src/lib/mergeGroupFeedItems.ts src/lib/createMenuItems.ts src/lib/groupCommunityTab.ts src/components/create src/components/buttons/createButton.tsx src/components/groups/detail/GroupPostsFeed.tsx src/components/groups/detail/GroupCreateMenu.tsx src/components/groups/detail/GroupCommunity.tsx src/app/groups/[slug]/GroupDetailClient.tsx
```

Expected: 0 errors

- [ ] **Step 3: Manual browser checklist**

1. Site Create: Discussion first, then Post.
2. Public group as manager: header Create → Discussion focuses Posts composer with Public / Members only.
3. Post a members-only discussion; appears on Posts; not on site `/discussions`.
4. Post a public discussion; appears on Posts and site feed with “in Group”.
5. `#discussions` opens Posts (no Discussions tab).
6. Non-member on public group: join-to-post; public items visible.
7. Event item still goes to group `events/new` with no audience radios.
8. 390px: menus and mixed feed usable.

- [ ] **Step 4: Commit any verification fixes only if needed**

---

## Spec coverage self-review

| Spec requirement | Task |
| --- | --- |
| Mixed chronological Posts feed | 1, 5 |
| Remove Discussions tab; `#discussions` → posts | 4 |
| Site Create Discussion then Post | 2, 3 |
| Group Create dropdown Discussion / Post / Event | 2, 6 |
| Discussion stays on group + composer | 5, 6 |
| Event unchanged (no audience) | 6 |
| Shared create module DRY | 2, 3, 6 |
| Single-column layout | 5 |
| Partial error / empty / loading | 5 |
| Pagination v1 window + blog load more | 5 |
| Empty copy update | 5 |
| No backend API | all |
| Tests | 1–7 |

## Placeholder scan

No TBD steps. Icon name for discussion must be resolved against `IconName` in Task 2 (concrete fallback: same icon as Discussions nav item).

## Type consistency

- `CreateMenuVariant` / `CreateMenuItemId` shared across Tasks 2–3–6.
- `focusDiscussionToken` / `forcePostsToken` as `number` counters in Tasks 5–6.
- `GroupFeedItem.kind` `'blog' | 'discussion'` only.
