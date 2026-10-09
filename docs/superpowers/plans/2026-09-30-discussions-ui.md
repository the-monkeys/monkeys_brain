# Discussions UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a text discussion feed to the left navigation and a group Discussions tab, both using one component and the existing discussion API.

**Architecture:** The Next app already has a desktop left sidebar (`FeedSidebarDesktop`) and a group community tab strip (`GroupCommunity`). The sidebar is 76–265px and hidden below `lg`, so the feed does not render inside the rail. The rail gains a Discussions link. The feed renders in the main column at `/discussions` and, unchanged, inside the group tab. One `DiscussionFeed` takes a scope of `site` or `group`. Site and group results never share a query cache.

**Tech Stack:** Next.js app `the_monkeys`, React Query, Vitest, existing axios clients, existing `TextTabs`.

**App root:** `c:\Users\Dave\the_monkeys\the_monkeys_engine\.pnpm-store\v11\projects\14d6bf0e9b4083dc1abbf1b0c7642df3\apps\the_monkeys`

All paths below are relative to that root. Run Vitest from that root.

## Global Constraints

- Body max is 500 Unicode code points after trim. Empty body is rejected in the composer. Do not send `files`. Private and unlisted groups are text only, and there is no upload route.
- Page size is 20. The next page query is `before_id` set to the last `public_id`.
- Site feed is `GET /api/v1/discussions` (`group_id` is null on the server). Group feed is `GET /api/v1/groups/:slug/discussions`. Never render a group post in the site list.
- Reads use `axiosInstanceNoAuth`. Writes use `axiosInstance` (login cookie). A 404 on a discussion or a private group list is "not found", never a forbidden toast.
- Group default tab stays `posts`. `/groups/:slug` stays hash-free. Existing hashes `#events`, `#about`, `#members`, `#requests`, `#invites` keep their meaning. `#blogs` still aliases to posts.
- Do not import the blog editor, EditorJS, or `GroupBlogsPanel` into discussions.
- Desktop left rail stays `hidden lg:block`. Do not add a sixth item to `MobileBottomTabBar`. The discussion page uses the main column, which already clears the mobile tab bar.
- Do not change blog, event, or feed routes. New routes are `/discussions` and `/discussions/[id]` only.
- Reply notifications and image attach are out of this plan.

---

### Task 1: Discussion types and rune counter

**Files:**
- Create: `src/services/discussions/discussionsTypes.ts`
- Create: `src/services/discussions/discussionText.ts`
- Test: `__tests__/src/services/discussions/discussionText.test.ts`

**Interfaces:**
- Consumes: nothing
- Produces: `Discussion`, `DiscussionReply`, `DiscussionList`, `DISCUSSION_MAX_RUNES`, `discussionRuneCount(body: string): number`, `discussionTextError(body: string): string | null`

- [ ] **Step 1: Write the failing test**

```ts
import { discussionRuneCount, discussionTextError } from '@/services/discussions/discussionText';
import { describe, expect, it } from 'vitest';

describe('discussionText', () => {
  it('counts Unicode code points after trim', () => {
    expect(discussionRuneCount('  hi  ')).toBe(2);
    expect(discussionRuneCount('a😀b')).toBe(3);
  });

  it('rejects an empty body and a body over 500', () => {
    expect(discussionTextError('   ')).toBe('Write something.');
    expect(discussionTextError('a'.repeat(501))).toBe(
      'A discussion can be at most 500 characters.'
    );
    expect(discussionTextError('hello')).toBeNull();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/services/discussions/discussionText.test.ts`

Expected: FAIL with "Failed to resolve import" or "discussionRuneCount is not a function".

- [ ] **Step 3: Write minimal implementation**

`src/services/discussions/discussionText.ts`:

```ts
export const DISCUSSION_MAX_RUNES = 500;

export function discussionRuneCount(body: string): number {
  return Array.from(body.trim()).length;
}

export function discussionTextError(body: string): string | null {
  const count = discussionRuneCount(body);
  if (count === 0) return 'Write something.';
  if (count > DISCUSSION_MAX_RUNES) {
    return 'A discussion can be at most 500 characters.';
  }
  return null;
}
```

`src/services/discussions/discussionsTypes.ts`:

```ts
export type DiscussionFile = {
  storage_key: string;
  content_type: string;
};

export type DiscussionReply = {
  public_id: string;
  parent_public_id: string;
  body: string;
  status: string;
  author_username: string;
  author_gone: boolean;
  created_at?: string;
};

export type Discussion = {
  public_id: string;
  body: string;
  status: string;
  author_username: string;
  author_gone: boolean;
  group_slug: string;
  reply_count: number;
  like_count: number;
  liked: boolean;
  created_at?: string;
  edited_until?: string;
  files?: DiscussionFile[];
  replies?: DiscussionReply[];
};

export type DiscussionList = {
  discussions: Discussion[];
};

export type DiscussionScope =
  | { kind: 'site' }
  | { kind: 'group'; slug: string };
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/services/discussions/discussionText.test.ts`

Expected: PASS, 2 tests.

- [ ] **Step 5: Commit**

```bash
git add src/services/discussions/discussionsTypes.ts src/services/discussions/discussionText.ts __tests__/src/services/discussions/discussionText.test.ts
git commit -m "feat: add discussion text limits"
```

---

### Task 2: API client

**Files:**
- Create: `src/services/discussions/discussionsApi.ts`
- Test: `__tests__/src/services/discussions/discussionsApi.test.ts`

**Interfaces:**
- Consumes: `Discussion`, `DiscussionList`, `DiscussionReply`, `DiscussionScope` from Task 1
- Produces:
  - `listDiscussions(scope: DiscussionScope, beforeId?: string): Promise<DiscussionList>`
  - `getDiscussion(id: string): Promise<Discussion>`
  - `createDiscussion(scope: DiscussionScope, body: string): Promise<Discussion>`
  - `replyToDiscussion(id: string, body: string, parentId?: string): Promise<DiscussionReply>`
  - `likeDiscussion(id: string): Promise<{ liked: boolean }>`
  - `editDiscussion(id: string, body: string): Promise<Discussion>`
  - `deleteDiscussion(id: string): Promise<Discussion>`
  - `discussionError(err: unknown): string`

- [ ] **Step 1: Write the failing test**

Mock axios the same way other service tests in this app mock it. Assert the URLs, not the network.

```ts
import { listDiscussions } from '@/services/discussions/discussionsApi';
import axiosInstanceNoAuth from '@/services/api/axiosInstanceNoAuth';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('@/services/api/axiosInstanceNoAuth', () => ({
  default: { get: vi.fn() },
}));

describe('listDiscussions', () => {
  beforeEach(() => {
    vi.mocked(axiosInstanceNoAuth.get).mockReset();
  });

  it('loads the site feed with a cursor and never a group slug', async () => {
    vi.mocked(axiosInstanceNoAuth.get).mockResolvedValue({
      data: { discussions: [] },
    });
    await listDiscussions({ kind: 'site' }, 'abc');
    expect(axiosInstanceNoAuth.get).toHaveBeenCalledWith(
      '/discussions?before_id=abc'
    );
  });

  it('loads a group feed on the group path', async () => {
    vi.mocked(axiosInstanceNoAuth.get).mockResolvedValue({
      data: { discussions: [] },
    });
    await listDiscussions({ kind: 'group', slug: 'tea club' });
    expect(axiosInstanceNoAuth.get).toHaveBeenCalledWith(
      '/groups/tea%20club/discussions'
    );
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/services/discussions/discussionsApi.test.ts`

Expected: FAIL because `listDiscussions` is not defined.

- [ ] **Step 3: Write minimal implementation**

```ts
import axiosInstance from '@/services/api/axiosInstance';
import axiosInstanceNoAuth from '@/services/api/axiosInstanceNoAuth';
import axios from 'axios';

import {
  Discussion,
  DiscussionList,
  DiscussionReply,
  DiscussionScope,
} from './discussionsTypes';

const seg = (v: string) => encodeURIComponent(v);

function listPath(scope: DiscussionScope, beforeId?: string): string {
  const base =
    scope.kind === 'site'
      ? '/discussions'
      : `/groups/${seg(scope.slug)}/discussions`;
  if (!beforeId) return base;
  return `${base}?before_id=${seg(beforeId)}`;
}

export function discussionError(err: unknown): string {
  if (axios.isAxiosError(err)) {
    const data = err.response?.data as { error?: string; message?: string } | undefined;
    return data?.error || data?.message || 'Something went wrong.';
  }
  return 'Something went wrong.';
}

export const listDiscussions = (scope: DiscussionScope, beforeId?: string) =>
  axiosInstanceNoAuth
    .get<DiscussionList>(listPath(scope, beforeId))
    .then((r) => r.data);

export const getDiscussion = (id: string) =>
  axiosInstanceNoAuth
    .get<Discussion>(`/discussions/${seg(id)}`)
    .then((r) => r.data);

export const createDiscussion = (scope: DiscussionScope, body: string) => {
  const path =
    scope.kind === 'site'
      ? '/discussions'
      : `/groups/${seg(scope.slug)}/discussions`;
  return axiosInstance
    .post<Discussion>(path, { body, files: [] })
    .then((r) => r.data);
};

export const replyToDiscussion = (id: string, body: string, parentId = '') =>
  axiosInstance
    .post<DiscussionReply>(`/discussions/${seg(id)}/replies`, {
      body,
      parent_reply_id: parentId,
    })
    .then((r) => r.data);

export const likeDiscussion = (id: string) =>
  axiosInstance
    .post<{ liked: boolean }>(`/discussions/${seg(id)}/like`)
    .then((r) => r.data);

export const editDiscussion = (id: string, body: string) =>
  axiosInstance
    .put<Discussion>(`/discussions/${seg(id)}`, { body })
    .then((r) => r.data);

export const deleteDiscussion = (id: string) =>
  axiosInstance
    .delete<Discussion>(`/discussions/${seg(id)}`)
    .then((r) => r.data);
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/services/discussions/discussionsApi.test.ts`

Expected: PASS, 2 tests.

- [ ] **Step 5: Commit**

```bash
git add src/services/discussions/discussionsApi.ts __tests__/src/services/discussions/discussionsApi.test.ts
git commit -m "feat: add discussion API client"
```

---

### Task 3: Query keys and hooks

**Files:**
- Modify: `src/lib/queryKeys.ts` (add a `discussions` key next to `groups`)
- Create: `src/hooks/discussions/useDiscussionQueries.ts`
- Test: `__tests__/src/lib/discussionQueryKeys.test.ts`

**Interfaces:**
- Consumes: `listDiscussions`, `createDiscussion`, `DiscussionScope` from Task 2
- Produces: `queryKeys.discussions.feed(scope)`, `useDiscussionFeed(scope)`, `useCreateDiscussion(scope)`

- [ ] **Step 1: Write the failing test**

```ts
import { queryKeys } from '@/lib/queryKeys';
import { describe, expect, it } from 'vitest';

describe('discussion query keys', () => {
  it('keeps the site feed and a group feed in different caches', () => {
    const site = queryKeys.discussions.feed({ kind: 'site' });
    const group = queryKeys.discussions.feed({ kind: 'group', slug: 'tea' });
    expect(site).not.toEqual(group);
    expect(site[1]).toBe('site');
    expect(group).toContain('tea');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/lib/discussionQueryKeys.test.ts`

Expected: FAIL because `queryKeys.discussions` is undefined.

- [ ] **Step 3: Write minimal implementation**

In `src/lib/queryKeys.ts`, add the import type and the key. Do not change `blog`, `events`, or `groups` keys.

```ts
import type { DiscussionScope } from '@/services/discussions/discussionsTypes';

const discussionRoot = ['discussions'] as const;

// inside queryKeys:
discussions: {
  all: discussionRoot,
  feed: (scope: DiscussionScope) =>
    scope.kind === 'site'
      ? ([...discussionRoot, 'site'] as const)
      : ([...discussionRoot, 'group', scope.slug] as const),
  detail: (id: QueryId) => [...discussionRoot, 'detail', id] as const,
},
```

`src/hooks/discussions/useDiscussionQueries.ts`:

```ts
'use client';

import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';

import { queryKeys } from '@/lib/queryKeys';
import {
  createDiscussion,
  listDiscussions,
} from '@/services/discussions/discussionsApi';
import { DiscussionScope } from '@/services/discussions/discussionsTypes';

const PAGE = 20;

export function useDiscussionFeed(scope: DiscussionScope, enabled = true) {
  return useInfiniteQuery({
    queryKey: queryKeys.discussions.feed(scope),
    queryFn: ({ pageParam }) => listDiscussions(scope, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => {
      if (!last.discussions || last.discussions.length < PAGE) return undefined;
      return last.discussions[last.discussions.length - 1]?.public_id;
    },
    enabled,
  });
}

export function useCreateDiscussion(scope: DiscussionScope) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (body: string) => createDiscussion(scope, body),
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: queryKeys.discussions.feed(scope) });
    },
  });
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/lib/discussionQueryKeys.test.ts`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/lib/queryKeys.ts src/hooks/discussions/useDiscussionQueries.ts __tests__/src/lib/discussionQueryKeys.test.ts
git commit -m "feat: cache site and group discussions separately"
```

---

### Task 4: Shared feed component

**Files:**
- Create: `src/components/discussions/DiscussionFeed.tsx`
- Create: `src/components/discussions/DiscussionComposer.tsx`
- Test: `__tests__/src/components/discussions/DiscussionComposer.test.tsx`

**Interfaces:**
- Consumes: `useDiscussionFeed`, `useCreateDiscussion`, `discussionTextError`, `DISCUSSION_MAX_RUNES`, `discussionError`
- Produces: `DiscussionFeed({ scope, canWrite, lockedMessage }: { scope: DiscussionScope; canWrite: boolean; lockedMessage: string })`

`canWrite` is decided by the caller. The site page passes `!!session` and `lockedMessage` "Log in to post a discussion." The group tab passes `isActiveGroupMember(group)`. When the viewer is logged out, the group message is the login sentence. When they are logged in and not a member, it is "Join the group to post a discussion." The component does not fetch group membership itself.

- [ ] **Step 1: Write the failing test**

```tsx
import { DiscussionComposer } from '@/components/discussions/DiscussionComposer';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

describe('DiscussionComposer', () => {
  it('shows the remaining count and blocks an empty post', async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn();
    render(
      <DiscussionComposer
        canWrite
        lockedMessage='Log in to post a discussion.'
        onSubmit={onSubmit}
        pending={false}
      />
    );
    expect(screen.getByText('500')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Post' }));
    expect(onSubmit).not.toHaveBeenCalled();
    expect(screen.getByText('Write something.')).toBeInTheDocument();
  });

  it('asks a logged-out reader to log in instead of posting', () => {
    render(
      <DiscussionComposer
        canWrite={false}
        lockedMessage='Log in to post a discussion.'
        onSubmit={vi.fn()}
        pending={false}
      />
    );
    expect(screen.queryByRole('button', { name: 'Post' })).not.toBeInTheDocument();
    expect(screen.getByText('Log in to post a discussion.')).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/components/discussions/DiscussionComposer.test.tsx`

Expected: FAIL because the component does not exist.

- [ ] **Step 3: Write minimal implementation**

`src/components/discussions/DiscussionComposer.tsx`:

```tsx
'use client';

import { useState } from 'react';

import {
  DISCUSSION_MAX_RUNES,
  discussionRuneCount,
  discussionTextError,
} from '@/services/discussions/discussionText';

export function DiscussionComposer({
  canWrite,
  lockedMessage,
  onSubmit,
  pending,
}: {
  canWrite: boolean;
  lockedMessage: string;
  onSubmit: (body: string) => void;
  pending: boolean;
}) {
  const [value, setValue] = useState('');
  const [error, setError] = useState<string | null>(null);
  if (!canWrite) {
    return <p>{lockedMessage}</p>;
  }
  const remaining = DISCUSSION_MAX_RUNES - discussionRuneCount(value);
  return (
    <form
      className='flex w-full min-w-0 flex-col gap-2'
      onSubmit={(event) => {
        event.preventDefault();
        const next = discussionTextError(value);
        setError(next);
        if (next) return;
        onSubmit(value.trim());
        setValue('');
      }}
    >
      <label className='sr-only' htmlFor='discussion-body'>
        Discussion
      </label>
      <textarea
        id='discussion-body'
        className='min-h-24 w-full'
        value={value}
        onChange={(event) => setValue(event.target.value)}
      />
      <div className='flex items-center justify-between gap-3'>
        <span>{remaining}</span>
        <button type='submit' disabled={pending}>
          Post
        </button>
      </div>
      {error ? <p>{error}</p> : null}
    </form>
  );
}
```

`DiscussionFeed` calls `useDiscussionFeed(scope)` and `useCreateDiscussion(scope)`. Flatten `data.pages` into one list. Each row links to `/discussions/${public_id}` and shows `author_gone ? 'Deleted account' : author_username`, the body, `reply_count`, and `like_count`. The container is `flex w-full min-w-0 flex-col gap-3`. A More button calls `fetchNextPage` only when `hasNextPage` is true. While `isLoading`, render the existing `Loader`. If the error is an axios 404, render "Nothing here." For any other error, render `discussionError(error)`. Pass `canWrite`, `lockedMessage`, and `mutate` into `DiscussionComposer`. No file input.

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/components/discussions/DiscussionComposer.test.tsx`

Expected: PASS, 2 tests.

- [ ] **Step 5: Commit**

```bash
git add src/components/discussions/DiscussionFeed.tsx src/components/discussions/DiscussionComposer.tsx __tests__/src/components/discussions/DiscussionComposer.test.tsx
git commit -m "feat: add a shared discussion feed"
```

---

### Task 5: Left navigation and site page

**Files:**
- Modify: `src/constants/routeConstants.ts` (add `DISCUSSIONS_ROUTE` and one `DISCOVER_ITEMS` entry)
- Create: `src/app/discussions/page.tsx`
- Test: extend `__tests__` only if a route-constants test already exists; otherwise the composer tests plus a manual check are enough. Add this assertion if `routeConstants` is already unit-tested: Discussions href is `/discussions` and `requiresAuth` is omitted so logged-out readers can open the feed.

**Interfaces:**
- Consumes: `DiscussionFeed` from Task 4
- Produces: route `/discussions` and a left-nav item labeled Discussions

- [ ] **Step 1: Write the failing test**

If no route test file exists, create `__tests__/src/constants/discussionsRoute.test.ts`:

```ts
import { DISCOVER_ITEMS, DISCUSSIONS_ROUTE } from '@/constants/routeConstants';
import { describe, expect, it } from 'vitest';

describe('discussions nav', () => {
  it('adds a public Discussions link without removing For You', () => {
    expect(DISCUSSIONS_ROUTE).toBe('/discussions');
    const labels = DISCOVER_ITEMS.map((item) => item.label);
    expect(labels).toContain('For You');
    expect(labels).toContain('Discussions');
    const item = DISCOVER_ITEMS.find((entry) => entry.href === '/discussions');
    expect(item?.requiresAuth).toBeUndefined();
    expect(item?.icon).toBe('RiChat1');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/constants/discussionsRoute.test.ts`

Expected: FAIL because `DISCUSSIONS_ROUTE` is not exported.

- [ ] **Step 3: Write minimal implementation**

In `routeConstants.ts`:

```ts
export const DISCUSSIONS_ROUTE = '/discussions';
```

Append this object to `DISCOVER_ITEMS` after Events. Do not reorder For You, Library, Settings, or Topics.

```ts
{ href: DISCUSSIONS_ROUTE, label: 'Discussions', icon: 'RiChat1' },
```

`src/app/discussions/page.tsx`:

```tsx
'use client';

import { DiscussionFeed } from '@/components/discussions/DiscussionFeed';
import useAuth from '@/hooks/auth/useAuth';

export default function DiscussionsPage() {
  const { data: session } = useAuth();
  return (
    <main className='mx-auto w-full min-w-0 max-w-2xl'>
      <h1 className='mb-4 text-xl font-semibold'>Discussions</h1>
      <DiscussionFeed
        scope={{ kind: 'site' }}
        canWrite={!!session}
        lockedMessage='Log in to post a discussion.'
      />
    </main>
  );
}
```

`max-w-2xl` keeps the column readable on a wide desktop. `w-full min-w-0` lets it shrink beside the 265px sidebar and go full width when the sidebar is hidden below `lg`. Do not edit `MobileBottomTabBar.tsx`.

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/constants/discussionsRoute.test.ts`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/constants/routeConstants.ts src/app/discussions/page.tsx __tests__/src/constants/discussionsRoute.test.ts
git commit -m "feat: link discussions from the left navigation"
```

---

### Task 6: Group Discussions tab

**Files:**
- Modify: `src/lib/groupCommunityTab.ts`
- Modify: `src/components/groups/detail/GroupCommunity.tsx`
- Modify: `__tests__/src/lib/groupCommunityTab.test.ts`

**Interfaces:**
- Consumes: `DiscussionFeed`, `isActiveGroupMember` from `@/lib/groupPerms`
- Produces: hash `#discussions` as a `GroupCommunityTab`

- [ ] **Step 1: Write the failing test**

Update the member-tab expectation and add one case. The current test expects exactly `['posts', 'events', 'about', 'members']`.

```ts
expect(groupCommunityTabs(false)).toEqual([
  'posts',
  'events',
  'discussions',
  'about',
  'members',
]);
expect(parseGroupCommunityTab('#discussions', false)).toBe('discussions');
expect(groupCommunityHash('discussions')).toBe('#discussions');
expect(parseGroupCommunityTab('', false)).toBe('posts');
```

Keep the existing assertions that `#blogs` is posts, `#invites` without staff is posts, and the default location has no hash.

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/lib/groupCommunityTab.test.ts`

Expected: FAIL because `discussions` is not a tab.

- [ ] **Step 3: Write minimal implementation**

Insert `'discussions'` after `'events'` in `GROUP_COMMUNITY_TABS`. Do not add it to `STAFF_ONLY_TABS`. Do not change `DEFAULT_GROUP_COMMUNITY_TAB`.

In `GroupCommunity.tsx`, add `discussions: 'Discussions'` to `TAB_LABELS` and this branch before the events branch:

```tsx
) : tab === 'discussions' ? (
  <DiscussionFeed
    scope={{ kind: 'group', slug: group.slug }}
    canWrite={isActiveGroupMember(group)}
    lockedMessage={
      session
        ? 'Join the group to post a discussion.'
        : 'Log in to post a discussion.'
    }
  />
```

`GroupCommunity` already returns null when `!canView && !staff`, so a private or unlisted non-member never sees the tab. Do not add a second visibility check that returns 403 copy.

Leave the Posts branch on `GroupBlogsPanel`.

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/lib/groupCommunityTab.test.ts __tests__/src/components/groups/detail/GroupCommunity.test.tsx`

Expected: PASS. If `GroupCommunity.test.tsx` snapshots the tab labels, update that snapshot to include Discussions and re-run.

- [ ] **Step 5: Commit**

```bash
git add src/lib/groupCommunityTab.ts src/components/groups/detail/GroupCommunity.tsx __tests__/src/lib/groupCommunityTab.test.ts
git commit -m "feat: add a group discussions tab"
```

---

### Task 7: Thread page

**Files:**
- Create: `src/app/discussions/[id]/page.tsx`
- Create: `src/components/discussions/DiscussionThread.tsx`
- Modify: `src/hooks/discussions/useDiscussionQueries.ts` (add `useDiscussion`, `useReplyToDiscussion`, `useLikeDiscussion`)

**Interfaces:**
- Consumes: `getDiscussion`, `replyToDiscussion`, `likeDiscussion`, `discussionTextError`, `discussionError`
- Produces: route `/discussions/[id]`

- [ ] **Step 1: Write the failing test**

```tsx
import { replyIndent } from '@/components/discussions/DiscussionThread';
import { describe, expect, it } from 'vitest';

describe('replyIndent', () => {
  it('indents only a reply to a reply', () => {
    expect(replyIndent('')).toBe(false);
    expect(replyIndent('parent')).toBe(true);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/components/discussions/DiscussionThread.test.tsx`

Expected: FAIL because `replyIndent` is not exported.

- [ ] **Step 3: Write minimal implementation**

```ts
export function replyIndent(parentPublicId: string): boolean {
  return parentPublicId.trim().length > 0;
}
```

`DiscussionThread` loads `useDiscussion(id)`. A 404 renders "Discussion not found." and nothing else. Replies render in order. A reply with a parent id is indented one step (`pl-6`). There is no third level of indent. The reply composer calls `discussionTextError` and posts with `parent_reply_id` only when the user pressed Reply on a top-level reply. Replying to an indented reply is not offered.

Like is one button. Its label is "Liked" or "Like" from `discussion.liked`. Edit and delete buttons render only when `session.username === author_username` and `author_gone` is false. Edit stays disabled when `edited_until` is in the past. Hide is not in this task.

The page is:

```tsx
'use client';

import { DiscussionThread } from '@/components/discussions/DiscussionThread';
import { useParams } from 'next/navigation';

export default function DiscussionPage() {
  const params = useParams<{ id: string }>();
  return (
    <main className='mx-auto w-full min-w-0 max-w-2xl'>
      <DiscussionThread id={params.id} />
    </main>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/components/discussions/DiscussionThread.test.tsx`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add src/app/discussions/[id]/page.tsx src/components/discussions/DiscussionThread.tsx src/hooks/discussions/useDiscussionQueries.ts __tests__/src/components/discussions/DiscussionThread.test.tsx
git commit -m "feat: add a discussion thread page"
```

---

## Spec coverage

| Spec item | Task |
| --- | --- |
| Site feed and `/discussions/:id` | Tasks 5 and 7 |
| Group tab next to Events / About / Members | Task 6, inserted after Events |
| Private group tab hidden from non-members | Task 6, existing `GroupCommunity` null return |
| Composer 500 and remaining count | Tasks 1 and 4 |
| Image attach | Not in this plan. The API has no upload route. |
| Do not reuse the blog editor | Tasks 4 and 6 use a textarea |
| Left panel | Task 5 adds the nav item. The feed stays in the main column so the collapsed 76px rail and the mobile layout stay intact |
| Group posts never on the site feed | Tasks 2 and 3 use separate URLs and query keys |

## Verification before calling the UI done

From the Next app root, with migration `000023` applied and `the_monkeys_discussions` listening on port 50064:

1. Desktop width: Discussions appears in the left rail. `/discussions` shows the site feed in the main column. The rail still collapses and the page does not overflow.
2. Below `lg`: the left rail is hidden, the bottom tab bar is unchanged, and `/discussions` is usable.
3. `/groups/:slug` still opens Posts with no hash. `#discussions` shows only that group's posts. A public group's stranger sees the list and the login sentence. A member sees the composer.
4. A private group, logged out, still hides the whole community panel.
5. `GET /discussions` in the network panel contains no `group_slug` on the returned posts.
