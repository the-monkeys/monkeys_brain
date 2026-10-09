# Discussions UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let people open, read, and post discussions from the left navigation and from a group tab, with each public discussion rendered in the first HTML response so search engines and AI fetchers can read it.

**Architecture:** The site feed and each thread are server pages. The first 20 posts and the thread body are in the server HTML, with JSON-LD and metadata. Client code is limited to posting, replying, liking, editing, deleting, and loading older posts. Group browsing stays inside the existing group tab and links each post to the same `/discussions/[id]` URL. Private and unlisted threads are not indexed. A stranger receives the Next.js not-found page because the API returns 404.

**Tech Stack:** Next.js 14 app `the_monkeys`, React 18, Vitest, Testing Library, existing `pageMetadata`, `JsonLd`, `normalizeSeoText`, and the discussion API client.

**App root:** `c:\Users\Dave\the_monkeys\the_monkeys_engine\local\the_monkeys\apps\the_monkeys`

**Git root:** `c:\Users\Dave\the_monkeys\the_monkeys_engine\local\the_monkeys` on branch `feature/discussions`.

All source paths below are relative to the app root. Run Vitest from the app root. This plan replaces the unfinished tasks in `docs/superpowers/plans/2026-09-30-discussions-ui.md`. These commits already exist and must not be redone:

- `2e3d69fb` adds `discussionText.ts` and `discussionsTypes.ts`
- `f5efdff1` adds `discussionsApi.ts`

## Global Constraints

- Body max is 500 Unicode code points after trim. Empty body is rejected in the composer. `createDiscussion` posts `{ body }` only. Do not send `files`.
- Do not render `files` from the API. There is no upload route and no image attach control.
- Page size is 20. The next page query is `before_id` set to the last `public_id`.
- Site feed is `GET /discussions` through `axiosInstanceNoAuth` or a cookie-less `fetch` to `API_URL`. Group feed is `GET /groups/:slug/discussions`. Never show a group post in the site list.
- Reads that a logged-out visitor may make use no session cookie. A member-only read forwards the request cookie from the server. A 404 is "not found", never a forbidden message.
- Public site posts and public group posts are indexable. Private, unlisted, deleted, and hidden posts are not. Unpublished groups stay hidden.
- Group default tab stays `posts`. `/groups/:slug` stays hash-free. Existing hashes `#events`, `#about`, `#members`, `#requests`, `#invites` keep their meaning. `#blogs` still aliases to posts. The new hash is `#discussions`.
- Do not import the blog editor, EditorJS, or `GroupBlogsPanel` into discussions.
- Desktop left rail stays `hidden lg:block`. Do not add a sixth item to `MobileBottomTabBar`. Adding `DISCUSSIONS_ROUTE` to `DISCOVER_ITEMS` also puts it in `MobileNavDrawer`.
- New routes are `/discussions`, `/discussions/[id]`, and `/discussions/sitemap.xml` only.
- UI copy and metadata must not contain an em dash. Pass discussion text through `normalizeSeoText` before it becomes a title or description.
- The first page and the thread body render on the server. Do not load that first view in `useEffect`. Format times in UTC so server and client print the same string.
- Do not add a dependency. Reply notifications are out of this plan.

## File map

- `src/services/discussions/discussionsApi.ts`: create posts `{ body }` only.
- `src/lib/discussionSeo.ts`: headline, description, metadata, JSON-LD. No React.
- `src/lib/discussionTime.ts`: UTC label shared by server and client.
- `src/lib/discussionPageData.ts`: server `fetch` only. Not imported by client components.
- `src/components/discussions/DiscussionCard.tsx`: one post, no hooks.
- `src/components/discussions/DiscussionComposer.tsx`: client textarea.
- `src/components/discussions/DiscussionActions.tsx`: client like, edit, delete.
- `src/components/discussions/ReplyForm.tsx`: client reply box.
- `src/components/discussions/DiscussionListClient.tsx`: client load-more.
- `src/components/discussions/GroupDiscussions.tsx`: client group tab panel.
- `src/app/discussions/page.tsx`: server feed.
- `src/app/discussions/[id]/page.tsx`: server thread.
- `src/app/discussions/sitemap.ts`: public URLs only.
- `src/constants/routeConstants.ts`: left-nav link.
- `src/lib/groupCommunityTab.ts` and `GroupCommunity.tsx`: `#discussions`.
- `src/app/sitemap.ts`, `src/app/robots.ts`, `src/app/llms.txt/route.ts`: discovery.

---

### Task 1: Create payload sends body only

**Files:**
- Modify: `src/services/discussions/discussionsApi.ts`
- Test: `__tests__/src/services/discussions/discussionsApi.test.ts`

**Interfaces:**
- Consumes: `createDiscussion(scope: DiscussionScope, body: string)`
- Produces: `POST` body `{ body: string }` with no `files` key

- [ ] **Step 1: Write the failing test**

Add this test to `__tests__/src/services/discussions/discussionsApi.test.ts`. Mock `@/services/api/axiosInstance` the same way the file already mocks `axiosInstanceNoAuth`.

```ts
import axiosInstance from '@/services/api/axiosInstance';
import { createDiscussion } from '@/services/discussions/discussionsApi';

vi.mock('@/services/api/axiosInstance', () => ({
  default: { post: vi.fn() },
}));

it('posts the body and does not send files', async () => {
  vi.mocked(axiosInstance.post).mockResolvedValue({ data: { public_id: 'p1' } });
  await createDiscussion({ kind: 'site' }, 'hello');
  expect(axiosInstance.post).toHaveBeenCalledWith('/discussions', { body: 'hello' });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/services/discussions/discussionsApi.test.ts`

Expected: FAIL because the call includes `files: []`.

- [ ] **Step 3: Write minimal implementation**

In `createDiscussion`, change the post body to `{ body }`.

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/services/discussions/discussionsApi.test.ts`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add apps/the_monkeys/src/services/discussions/discussionsApi.ts apps/the_monkeys/__tests__/src/services/discussions/discussionsApi.test.ts
git commit -m "fix: post discussion text without a files field"
```

Run the commit from the UI git root.

---

### Task 2: SEO text and JSON-LD

**Files:**
- Create: `src/lib/discussionSeo.ts`
- Test: `__tests__/src/lib/discussionSeo.test.ts`

**Interfaces:**
- Consumes: `Discussion` from `@/services/discussions/discussionsTypes`, `normalizeSeoText`, `pageMetadata`, `absoluteUrl`, `MONKEYS_WEBSITE_ID`, `publisherOrg` from `@/lib/seo`
- Produces:
  - `discussionHeadline(body: string): string`
  - `discussionDescription(body: string): string`
  - `discussionAuthorName(username: string, gone: boolean): string`
  - `discussionPath(id: string): string`
  - `buildDiscussionMetadata(post: Discussion, indexable: boolean): Metadata`
  - `buildDiscussionJsonLd(post: Discussion): Record<string, unknown>`
  - `buildDiscussionFeedJsonLd(): Record<string, unknown>`

- [ ] **Step 1: Write the failing test**

```ts
import {
  buildDiscussionJsonLd,
  buildDiscussionMetadata,
  discussionAuthorName,
  discussionDescription,
  discussionHeadline,
} from '@/lib/discussionSeo';
import { Discussion } from '@/services/discussions/discussionsTypes';
import { describe, expect, it } from 'vitest';

const post: Discussion = {
  public_id: 'abc',
  body: 'Chai notes\nA short public thread.',
  status: 'visible',
  author_username: 'dave',
  author_gone: false,
  group_slug: '',
  reply_count: 1,
  like_count: 2,
  liked: false,
  created_at: '2026-10-05T04:00:00Z',
  replies: [
    {
      public_id: 'r1',
      parent_public_id: '',
      body: 'Agreed.',
      status: 'visible',
      author_username: 'lee',
      author_gone: false,
      created_at: '2026-10-05T05:00:00Z',
    },
  ],
};

describe('discussionSeo', () => {
  it('builds a short headline and strips an em dash', () => {
    expect(discussionHeadline('Hello \u2014 world')).toBe('Hello, world');
    expect(discussionHeadline('   ')).toBe('Discussion');
  });

  it('names a deleted author', () => {
    expect(discussionAuthorName('dave', false)).toBe('dave');
    expect(discussionAuthorName('', true)).toBe('Deleted account');
  });

  it('indexes a public post and hides a member-only post', () => {
    const pub = buildDiscussionMetadata(post, true);
    const priv = buildDiscussionMetadata(post, false);
    expect(pub.description).toBe(discussionDescription(post.body));
    expect(JSON.stringify(pub.robots)).toContain('"index":true');
    expect(JSON.stringify(priv.robots)).toContain('"index":false');
  });

  it('emits DiscussionForumPosting text in JSON-LD', () => {
    const data = buildDiscussionJsonLd(post);
    expect(data['@type']).toBe('DiscussionForumPosting');
    expect(data.articleBody).toBe('Chai notes\nA short public thread.');
    expect(data.url).toContain('/discussions/abc');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/lib/discussionSeo.test.ts`

Expected: FAIL with "Failed to resolve import".

- [ ] **Step 3: Write minimal implementation**

`src/lib/discussionSeo.ts`:

```ts
import type { Metadata } from 'next';

import {
  MONKEYS_WEBSITE_ID,
  absoluteUrl,
  noIndexRobots,
  normalizeSeoText,
  pageMetadata,
  publisherOrg,
} from '@/lib/seo';
import { Discussion } from '@/services/discussions/discussionsTypes';

export function discussionHeadline(body: string): string {
  return normalizeSeoText(body, 70) || 'Discussion';
}

export function discussionDescription(body: string): string {
  return (
    normalizeSeoText(body, 160) ||
    'A public discussion on Monkeys.'
  );
}

export function discussionAuthorName(username: string, gone: boolean): string {
  if (gone || !username.trim()) return 'Deleted account';
  return username.trim();
}

export function discussionPath(id: string): string {
  return `/discussions/${encodeURIComponent(id)}`;
}

export function buildDiscussionMetadata(
  post: Discussion,
  indexable: boolean
): Metadata {
  const title = `${discussionHeadline(post.body)} | Monkeys`;
  const description = discussionDescription(post.body);
  if (!indexable) {
    return {
      title: { absolute: title },
      description,
      robots: noIndexRobots,
    };
  }
  return pageMetadata({
    title,
    description,
    path: discussionPath(post.public_id),
    type: 'article',
  });
}

export function buildDiscussionJsonLd(post: Discussion): Record<string, unknown> {
  const url = absoluteUrl(discussionPath(post.public_id));
  const comments = (post.replies ?? [])
    .filter((reply) => reply.status === 'visible')
    .map((reply) => ({
      '@type': 'Comment',
      text: reply.body,
      url: `${url}#reply-${reply.public_id}`,
      author: {
        '@type': 'Person',
        name: discussionAuthorName(reply.author_username, reply.author_gone),
      },
      ...(reply.created_at ? { dateCreated: reply.created_at } : {}),
    }));

  return {
    '@context': 'https://schema.org',
    '@type': 'DiscussionForumPosting',
    headline: discussionHeadline(post.body),
    articleBody: post.body,
    url,
    mainEntityOfPage: url,
    ...(post.created_at ? { datePublished: post.created_at } : {}),
    author: {
      '@type': 'Person',
      name: discussionAuthorName(post.author_username, post.author_gone),
    },
    publisher: publisherOrg(),
    isPartOf: post.group_slug
      ? {
          '@type': 'WebPage',
          url: absoluteUrl(`/groups/${encodeURIComponent(post.group_slug)}`),
        }
      : { '@id': MONKEYS_WEBSITE_ID },
    interactionStatistic: [
      {
        '@type': 'InteractionCounter',
        interactionType: 'https://schema.org/LikeAction',
        userInteractionCount: post.like_count,
      },
      {
        '@type': 'InteractionCounter',
        interactionType: 'https://schema.org/CommentAction',
        userInteractionCount: post.reply_count,
      },
    ],
    ...(comments.length ? { comment: comments } : {}),
  };
}

export function buildDiscussionFeedJsonLd(): Record<string, unknown> {
  const url = absoluteUrl('/discussions');
  return {
    '@context': 'https://schema.org',
    '@type': 'CollectionPage',
    name: 'Monkeys Discussions',
    url,
    description:
      'Public discussions on Monkeys. Read a thread or start one.',
    isPartOf: { '@id': MONKEYS_WEBSITE_ID },
  };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/lib/discussionSeo.test.ts`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add apps/the_monkeys/src/lib/discussionSeo.ts apps/the_monkeys/__tests__/src/lib/discussionSeo.test.ts
git commit -m "feat: add discussion metadata and structured data"
```

---

### Task 3: UTC time label

**Files:**
- Create: `src/lib/discussionTime.ts`
- Test: `__tests__/src/lib/discussionTime.test.ts`

**Interfaces:**
- Consumes: nothing
- Produces: `discussionTimeLabel(iso?: string): string`

- [ ] **Step 1: Write the failing test**

```ts
import { discussionTimeLabel } from '@/lib/discussionTime';
import { describe, expect, it } from 'vitest';

describe('discussionTimeLabel', () => {
  it('formats UTC and ignores a bad value', () => {
    expect(discussionTimeLabel('2026-10-05T04:00:00Z')).toBe('5 Oct 2026');
    expect(discussionTimeLabel('nope')).toBe('');
    expect(discussionTimeLabel(undefined)).toBe('');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/lib/discussionTime.test.ts`

Expected: FAIL with "Failed to resolve import".

- [ ] **Step 3: Write minimal implementation**

```ts
export function discussionTimeLabel(iso?: string): string {
  if (!iso) return '';
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';
  return new Intl.DateTimeFormat('en-GB', {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    timeZone: 'UTC',
  }).format(date);
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/lib/discussionTime.test.ts`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add apps/the_monkeys/src/lib/discussionTime.ts apps/the_monkeys/__tests__/src/lib/discussionTime.test.ts
git commit -m "feat: format discussion times in UTC"
```

---

### Task 4: Server loader

**Files:**
- Create: `src/lib/discussionPageData.ts`
- Test: `__tests__/src/lib/discussionPageData.test.ts`

**Interfaces:**
- Consumes: `Discussion`, `DiscussionList`, `API_URL`
- Produces:
  - `export type DiscussionLoad = { state: 'ok'; discussion: Discussion; indexable: boolean } | { state: 'missing' } | { state: 'unavailable' }`
  - `loadDiscussion(id: string, cookieHeader: string): Promise<DiscussionLoad>`
  - `loadSiteDiscussions(beforeId?: string): Promise<DiscussionList | null>`
  - `loadPublicDiscussionIds(limit: number): Promise<string[]>`

The page passes `cookies().toString()` from `next/headers`. Tests call `loadDiscussion` with a cookie string so they do not import `next/headers`. `indexable` is true only when a cookie-less request returns 200 and `status === 'visible'`. A 404 from both requests is `missing`. Any other failure is `unavailable` and must not throw.

- [ ] **Step 1: Write the failing test**

```ts
import {
  loadDiscussion,
  loadPublicDiscussionIds,
} from '@/lib/discussionPageData';
import { beforeEach, describe, expect, it, vi } from 'vitest';

describe('discussionPageData', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', vi.fn());
  });

  it('marks a public post indexable', async () => {
    vi.mocked(fetch).mockResolvedValue(
      new Response(
        JSON.stringify({
          public_id: 'abc',
          body: 'Hello',
          status: 'visible',
          author_username: 'dave',
          author_gone: false,
          group_slug: '',
          reply_count: 0,
          like_count: 0,
          liked: false,
        }),
        { status: 200 }
      )
    );
    const loaded = await loadDiscussion('abc', '');
    expect(loaded).toMatchObject({ state: 'ok', indexable: true });
  });

  it('hides a post that only the member cookie can read', async () => {
    vi.mocked(fetch)
      .mockResolvedValueOnce(new Response('', { status: 404 }))
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            public_id: 'abc',
            body: 'Secret',
            status: 'visible',
            author_username: 'dave',
            author_gone: false,
            group_slug: 'tea',
            reply_count: 0,
            like_count: 0,
            liked: false,
          }),
          { status: 200 }
        )
      );
    const loaded = await loadDiscussion('abc', 'session=1');
    expect(loaded).toMatchObject({ state: 'ok', indexable: false });
  });

  it('returns missing when nobody can read it', async () => {
    vi.mocked(fetch).mockResolvedValue(new Response('', { status: 404 }));
    expect(await loadDiscussion('abc', '')).toEqual({ state: 'missing' });
  });

  it('collects only visible public ids and skips a failed group', async () => {
    vi.mocked(fetch)
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ discussions: [{ public_id: 's1', status: 'visible' }] }), {
          status: 200,
        })
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ groups: [{ slug: 'tea', visibility: 'public', status: 'published', id: 1, name: 'Tea' }] }), {
          status: 200,
        })
      )
      .mockResolvedValueOnce(new Response('', { status: 404 }));
    await expect(loadPublicDiscussionIds(20)).resolves.toEqual(['s1']);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/lib/discussionPageData.test.ts`

Expected: FAIL with "Failed to resolve import".

- [ ] **Step 3: Write minimal implementation**

`loadDiscussion` requests `${API_URL}/discussions/${encodeURIComponent(id)}` with `cache: 'no-store'`. First call sends no cookie. On 200, parse JSON and set `indexable` to `status === 'visible'`. On 404 with an empty cookie header, return `{ state: 'missing' }`. On 404 with a cookie, retry once with header `Cookie`. A 200 on that retry is `{ state: 'ok', indexable: false }`. A 404 on that retry is `missing`. If `API_URL` is empty, the response is not JSON, or the status is anything else, return `{ state: 'unavailable' }`.

`loadSiteDiscussions` calls `${API_URL}/discussions` plus `?before_id=` when present, with `cache: 'no-store'` and no cookie. Return the JSON on 200, otherwise `null`.

`loadPublicDiscussionIds` uses `next: { revalidate: 300 }`, no cookie, and never throws. Read the site list once. Then call `fetchPublicGroups(40)` from `@/lib/seoCatalog`. For each group, `GET /groups/${encodeURIComponent(slug)}/discussions`. Skip non-200 responses. Keep ids whose `status` is `visible`, stop at `limit`, and return unique ids. Site ids come first.

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/lib/discussionPageData.test.ts`

Expected: PASS

The public-id test must mock `API_URL` if the module reads it at call time. If `API_URL` is undefined in Vitest, guard the request URL so the test still asserts `fetch` was called and a thrown catalog error becomes `[]`.

- [ ] **Step 5: Commit**

```bash
git add apps/the_monkeys/src/lib/discussionPageData.ts apps/the_monkeys/__tests__/src/lib/discussionPageData.test.ts
git commit -m "feat: load discussions on the server without throwing"
```

---

### Task 5: Shared discussion card

**Files:**
- Create: `src/components/discussions/DiscussionCard.tsx`
- Test: `__tests__/src/components/discussions/DiscussionCard.test.tsx`

**Interfaces:**
- Consumes: `Discussion`, `discussionAuthorName`, `discussionPath`, `discussionTimeLabel`
- Produces: `DiscussionCard({ post, linked }: { post: Discussion; linked: boolean })`

The card has no hooks and no `'use client'`. It renders one `article`. The body is plain text in a `p` with `whitespace-pre-wrap`. The author is a link to `/${username}` when the author is not gone. The time uses `dateTime={post.created_at}` and the UTC label. When `linked` is true, the headline links to `discussionPath`. Replies are not rendered here.

- [ ] **Step 1: Write the failing test**

```tsx
import { render, screen } from '@testing-library/react';
import { DiscussionCard } from '@/components/discussions/DiscussionCard';
import { describe, expect, it } from 'vitest';

const post = {
  public_id: 'abc',
  body: 'Hello thread',
  status: 'visible',
  author_username: 'dave',
  author_gone: false,
  group_slug: '',
  reply_count: 0,
  like_count: 3,
  liked: false,
  created_at: '2026-10-05T04:00:00Z',
};

describe('DiscussionCard', () => {
  it('prints the body in the article', () => {
    render(<DiscussionCard post={post} linked />);
    expect(screen.getByRole('article').textContent).toContain('Hello thread');
    expect(screen.getByRole('link', { name: 'Hello thread' }).getAttribute('href')).toBe(
      '/discussions/abc'
    );
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/components/discussions/DiscussionCard.test.tsx`

Expected: FAIL with "Failed to resolve import".

- [ ] **Step 3: Write minimal implementation**

Use `font-inter text-base leading-relaxed`, `min-w-0`, and a block link. Like count is text, `3 likes`, not a button. Do not render files.

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/components/discussions/DiscussionCard.test.tsx`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add apps/the_monkeys/src/components/discussions/DiscussionCard.tsx apps/the_monkeys/__tests__/src/components/discussions/DiscussionCard.test.tsx
git commit -m "feat: render one discussion as an article"
```

---

### Task 6: Composer and actions

**Files:**
- Create: `src/components/discussions/DiscussionComposer.tsx`
- Create: `src/components/discussions/DiscussionActions.tsx`
- Create: `src/components/discussions/ReplyForm.tsx`
- Test: `__tests__/src/components/discussions/DiscussionComposer.test.tsx`

**Interfaces:**
- Consumes: `discussionTextError`, `createDiscussion`, `replyToDiscussion`, `likeDiscussion`, `editDiscussion`, `deleteDiscussion`, `discussionError`, `useAuth`, `useRouter` from `next/navigation`
- Produces:
  - `DiscussionComposer({ scope, lockedMessage }: { scope: DiscussionScope; lockedMessage: string | null })`
  - `DiscussionActions({ post }: { post: Discussion })`
  - `ReplyForm({ id }: { id: string })`

All three files start with `'use client'`. A locked composer renders the message and no textarea. An unlocked composer is a `textarea` with `rows={4}`, `maxLength={2000}`, and a submit button of class `min-h-11`. Submit calls `discussionTextError` first and shows that string. On success call `router.refresh()`. On failure show `discussionError(err)` in a `p role="alert"`. Do not throw.

`DiscussionActions` shows Like for a signed-in user. Edit and Delete render only when `session.username === post.author_username` and, for Edit, `edited_until` is a future ISO time. Edit uses a textarea and `editDiscussion`. Delete calls `deleteDiscussion` then `router.refresh()`.

`ReplyForm` posts `replyToDiscussion(id, body, '')`. One level of reply from this form is enough. The thread page passes `parentId` only when replying to a top-level reply:

- Produces also: `ReplyForm({ id, parentId }: { id: string; parentId?: string })`

- [ ] **Step 1: Write the failing test**

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { DiscussionComposer } from '@/components/discussions/DiscussionComposer';
import { describe, expect, it, vi } from 'vitest';

vi.mock('next/navigation', () => ({ useRouter: () => ({ refresh: vi.fn() }) }));
vi.mock('@/hooks/auth/useAuth', () => ({ default: () => ({ data: { username: 'dave' } }) }));
vi.mock('@/services/discussions/discussionsApi', () => ({
  createDiscussion: vi.fn(),
  discussionError: () => 'Something went wrong.',
}));

describe('DiscussionComposer', () => {
  it('shows the lock message instead of a box', () => {
    render(
      <DiscussionComposer
        scope={{ kind: 'site' }}
        lockedMessage='Log in to post a discussion.'
      />
    );
    expect(screen.getByText('Log in to post a discussion.')).toBeTruthy();
    expect(screen.queryByRole('textbox')).toBeNull();
  });

  it('rejects an empty post before calling the API', async () => {
    const api = await import('@/services/discussions/discussionsApi');
    render(<DiscussionComposer scope={{ kind: 'site' }} lockedMessage={null} />);
    await userEvent.click(screen.getByRole('button', { name: 'Post discussion' }));
    expect(screen.getByRole('alert').textContent).toBe('Write something.');
    expect(api.createDiscussion).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/components/discussions/DiscussionComposer.test.tsx`

Expected: FAIL with "Failed to resolve import".

- [ ] **Step 3: Write minimal implementation**

Button label is `Post discussion`. Reply button label is `Reply`. Like button label is `Like` or `Liked`. Copy contains no em dash.

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/components/discussions/DiscussionComposer.test.tsx`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add apps/the_monkeys/src/components/discussions/DiscussionComposer.tsx apps/the_monkeys/src/components/discussions/DiscussionActions.tsx apps/the_monkeys/src/components/discussions/ReplyForm.tsx apps/the_monkeys/__tests__/src/components/discussions/DiscussionComposer.test.tsx
git commit -m "feat: add discussion composer and post actions"
```

---

### Task 7: Site discussions page

**Files:**
- Create: `src/app/discussions/page.tsx`
- Create: `src/components/discussions/DiscussionListClient.tsx`
- Modify: `src/constants/routeConstants.ts`
- Test: `__tests__/src/components/discussions/DiscussionListClient.test.tsx`

**Interfaces:**
- Consumes: `loadSiteDiscussions`, `DiscussionCard`, `DiscussionComposer`, `buildDiscussionFeedJsonLd`, `pageMetadata`, `JsonLd`, `useAuth`
- Produces: route `/discussions` and `DISCUSSIONS_ROUTE = '/discussions'`

`page.tsx` is a server component. Export metadata with `pageMetadata`:

```ts
title: 'Discussions | Monkeys',
description: 'Public discussions on Monkeys. Read a thread or start one.',
path: '/discussions',
```

Render `JsonLd` with `buildDiscussionFeedJsonLd()`, then an `h1` whose text is `Discussions`. Call `loadSiteDiscussions()`. When the result is `null`, render `p` text `Discussions are temporarily unavailable.` and still render the composer shell only when the list loaded. When the list is empty, render `No discussions yet.`

Pass the first page into `DiscussionListClient`. That client component appends older pages by calling `listDiscussions({ kind: 'site' }, lastId)`. The button text is `Older discussions`, class `min-h-11`, and it hides when the last page returned fewer than 20 posts. A failed load shows `Could not load older discussions.` and does not clear the posts already on screen.

The page wrapper is `mx-auto w-full min-w-0 max-w-2xl pb-24 lg:pb-0`. The composer lock on this page is decided in the client list: signed out shows `Log in to post a discussion.` Signed in shows the textarea. Scope is `{ kind: 'site' }`.

Insert the nav item in `DISCOVER_ITEMS` immediately after the Events item:

```ts
export const DISCUSSIONS_ROUTE = '/discussions';

{ href: DISCUSSIONS_ROUTE, label: 'Discussions', icon: 'RiChat1' },
```

Do not set `requiresAuth`. Do not edit `MobileBottomTabBar.tsx`.

- [ ] **Step 1: Write the failing test**

```tsx
import { render, screen } from '@testing-library/react';
import { DiscussionListClient } from '@/components/discussions/DiscussionListClient';
import { describe, expect, it, vi } from 'vitest';

vi.mock('next/navigation', () => ({ useRouter: () => ({ refresh: vi.fn() }) }));
vi.mock('@/hooks/auth/useAuth', () => ({ default: () => ({ data: null }) }));

it('asks a logged-out reader to log in', () => {
  render(<DiscussionListClient initial={[]} />);
  expect(screen.getByText('Log in to post a discussion.')).toBeTruthy();
  expect(screen.getByText('No discussions yet.')).toBeTruthy();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/components/discussions/DiscussionListClient.test.tsx`

Expected: FAIL with "Failed to resolve import".

- [ ] **Step 3: Write minimal implementation**

Server page plus the client list plus the route constant.

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/components/discussions/DiscussionListClient.test.tsx`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add apps/the_monkeys/src/app/discussions/page.tsx apps/the_monkeys/src/components/discussions/DiscussionListClient.tsx apps/the_monkeys/src/constants/routeConstants.ts apps/the_monkeys/__tests__/src/components/discussions/DiscussionListClient.test.tsx
git commit -m "feat: add the public discussions page and nav link"
```

---

### Task 8: Thread page

**Files:**
- Create: `src/app/discussions/[id]/page.tsx`
- Create: `src/components/discussions/DiscussionThread.tsx`
- Test: `__tests__/src/components/discussions/DiscussionThread.test.tsx`

**Interfaces:**
- Consumes: `loadDiscussion`, `buildDiscussionMetadata`, `buildDiscussionJsonLd`, `DiscussionCard`, `DiscussionActions`, `ReplyForm`, `cookies` from `next/headers`, `notFound` from `next/navigation`
- Produces: `DiscussionThread({ post }: { post: Discussion })` and `generateMetadata`

`generateMetadata` calls `loadDiscussion(params.id, cookies().toString())`. `missing` returns `noIndexPage('Discussion not found | Monkeys')`. `unavailable` returns `noIndexPage('Discussion temporarily unavailable | Monkeys')`. `ok` returns `buildDiscussionMetadata(post, indexable)`.

The page calls the same loader. `missing` calls `notFound()`. `unavailable` renders the unavailable sentence and no composer. `ok` renders `JsonLd` only when `indexable` is true, then `DiscussionThread`.

`DiscussionThread` is a server component. It renders `DiscussionCard` with `linked={false}`, then `DiscussionActions`, then replies. Group replies by `parent_public_id`: top-level replies have an empty parent, and their children render one level under them. A third level is not rendered. Each reply is an `article` with `id={`reply-${public_id}`}` and the body in a `p`. Under each top-level reply, render `ReplyForm` with that `parentId`. Under the post, render `ReplyForm` with no parent. Signed-out reply forms still render; the API returns 401 and `ReplyForm` shows `discussionError`.

Wrapper class matches the feed: `mx-auto w-full min-w-0 max-w-2xl pb-24 lg:pb-0`.

- [ ] **Step 1: Write the failing test**

```tsx
import { render, screen } from '@testing-library/react';
import { DiscussionThread } from '@/components/discussions/DiscussionThread';
import { describe, expect, it, vi } from 'vitest';

vi.mock('@/components/discussions/DiscussionActions', () => ({
  DiscussionActions: () => null,
}));
vi.mock('@/components/discussions/ReplyForm', () => ({
  ReplyForm: () => null,
}));

it('prints the post and one reply level', () => {
  render(
    <DiscussionThread
      post={{
        public_id: 'abc',
        body: 'Root post',
        status: 'visible',
        author_username: 'dave',
        author_gone: false,
        group_slug: '',
        reply_count: 1,
        like_count: 0,
        liked: false,
        replies: [
          {
            public_id: 'r1',
            parent_public_id: '',
            body: 'First reply',
            status: 'visible',
            author_username: 'lee',
            author_gone: false,
          },
        ],
      }}
    />
  );
  expect(screen.getByText('Root post')).toBeTruthy();
  expect(screen.getByText('First reply')).toBeTruthy();
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/components/discussions/DiscussionThread.test.tsx`

Expected: FAIL with "Failed to resolve import".

- [ ] **Step 3: Write minimal implementation**

Include the page and the thread component.

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/components/discussions/DiscussionThread.test.tsx`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add apps/the_monkeys/src/app/discussions/[id]/page.tsx apps/the_monkeys/src/components/discussions/DiscussionThread.tsx apps/the_monkeys/__tests__/src/components/discussions/DiscussionThread.test.tsx
git commit -m "feat: add a server-rendered discussion thread"
```

---

### Task 9: Group discussions tab

**Files:**
- Modify: `src/lib/groupCommunityTab.ts`
- Modify: `src/components/groups/detail/GroupCommunity.tsx`
- Create: `src/components/discussions/GroupDiscussions.tsx`
- Test: `__tests__/src/lib/groupCommunityTab.test.ts`
- Test: `__tests__/src/components/discussions/GroupDiscussions.test.tsx`

**Interfaces:**
- Consumes: `listDiscussions`, `DiscussionComposer`, `DiscussionCard`, `isActiveGroupMember`
- Produces: tab id `discussions` after `events` and before `about`

Update `GROUP_COMMUNITY_TABS` to:

```ts
['posts', 'events', 'discussions', 'about', 'members', 'requests', 'invites']
```

`STAFF_ONLY_TABS` stays `requests` and `invites`. Default stays `posts`. Update the member expectation in `groupCommunityTab.test.ts` to include `discussions` after `events`. Add:

```ts
expect(parseGroupCommunityTab('#discussions', false)).toBe('discussions');
expect(groupCommunityHash('discussions')).toBe('#discussions');
expect(groupCommunityHash('posts')).toBe('');
```

In `GroupCommunity.tsx`, add `discussions: 'Discussions'` to `TAB_LABELS`. In the tab body, before the `about` branch:

```tsx
) : tab === 'discussions' ? (
  <GroupDiscussions group={group} />
```

Replace the comment above `GroupCommunity` so it does not use an em dash. Say posts lead, then events, discussions, about, members, and staff-only requests and invites.

`GroupDiscussions` is `'use client'`. On mount, `listDiscussions({ kind: 'group', slug: group.slug })`. Render each post with `DiscussionCard linked`. A 404 shows `This group is not available.` Any other error shows `Could not load discussions.` Empty shows `No discussions yet.`

Composer lock:

```ts
const signedIn = Boolean(session);
const member = isActiveGroupMember(group);
const lockedMessage = !signedIn
  ? 'Log in to post a discussion.'
  : !member
    ? 'Join the group to post a discussion.'
    : null;
```

Scope is `{ kind: 'group', slug: group.slug }`. After a successful post, reload the list. Do not change the default tab.

- [ ] **Step 1: Write the failing test**

Add the hash expectations to the existing group tab test, and add a GroupDiscussions test that mocks `listDiscussions` to reject with an Axios 404 and expects `This group is not available.`

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/lib/groupCommunityTab.test.ts __tests__/src/components/discussions/GroupDiscussions.test.tsx`

Expected: FAIL because `discussions` is not a tab and the component is missing.

- [ ] **Step 3: Write minimal implementation**

Tab constant, panel, and the one branch in `GroupCommunity`.

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/lib/groupCommunityTab.test.ts __tests__/src/components/discussions/GroupDiscussions.test.tsx`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add apps/the_monkeys/src/lib/groupCommunityTab.ts apps/the_monkeys/src/components/groups/detail/GroupCommunity.tsx apps/the_monkeys/src/components/discussions/GroupDiscussions.tsx apps/the_monkeys/__tests__/src/lib/groupCommunityTab.test.ts apps/the_monkeys/__tests__/src/components/discussions/GroupDiscussions.test.tsx
git commit -m "feat: add a group discussions tab"
```

---

### Task 10: Sitemap and AI discovery

**Files:**
- Create: `src/app/discussions/sitemap.ts`
- Modify: `src/app/sitemap.ts`
- Modify: `src/app/robots.ts`
- Modify: `src/app/llms.txt/route.ts`
- Test: `__tests__/src/app/discussions/sitemap.test.ts` only if `loadPublicDiscussionIds` is called from a pure helper. Otherwise test the helper added in this task.

**Interfaces:**
- Consumes: `loadPublicDiscussionIds`, `SITE_URL`, `discussionPath`
- Produces: `src/app/discussions/sitemap.ts` default export, static path `/discussions` on the main sitemap, sitemap URL in `robots.ts`, and two lines in `llms.txt`

Add `{ path: '/discussions', changeFrequency: 'daily', priority: 0.8 }` to `STATIC_PATHS` after `/groups`.

`src/app/discussions/sitemap.ts`:

```ts
import type { MetadataRoute } from 'next';

import { loadPublicDiscussionIds } from '@/lib/discussionPageData';
import { discussionPath } from '@/lib/discussionSeo';
import { SITE_URL } from '@/lib/seo';

export const revalidate = 300;

export default async function sitemap(): Promise<MetadataRoute.Sitemap> {
  try {
    const ids = await loadPublicDiscussionIds(200);
    return ids.map((id) => ({
      url: `${SITE_URL}${discussionPath(id)}`,
      changeFrequency: 'daily' as const,
      priority: 0.6,
    }));
  } catch {
    return [];
  }
}
```

Add `${baseUrl}/discussions/sitemap.xml` to the `sitemap` array in `robots.ts`. Do not disallow `/discussions`.

In `llms.txt` `BODY`, add under Public collections:

```txt
- Discussions: ${SITE_URL}/discussions
```

Add under Sitemaps:

```txt
- Discussion sitemap: ${SITE_URL}/discussions/sitemap.xml
```

- [ ] **Step 1: Write the failing test**

If `STATIC_PATHS` is not exported, export a function `discussionsSitemap(ids: string[], siteUrl: string)` from `src/lib/discussionSeo.ts` and test that `discussionsSitemap(['a b'], 'https://monkeys.com.co')` returns one URL `https://monkeys.com.co/discussions/a%20b`. Use that helper inside the sitemap route.

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run __tests__/src/lib/discussionSeo.test.ts`

Expected: FAIL because `discussionsSitemap` is missing.

- [ ] **Step 3: Write minimal implementation**

Helper, sitemap route, main sitemap entry, robots entry, and `llms.txt` lines.

- [ ] **Step 4: Run test to verify it passes**

Run: `npx vitest --run __tests__/src/lib/discussionSeo.test.ts __tests__/src/services/discussions/discussionsApi.test.ts __tests__/src/lib/groupCommunityTab.test.ts`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add apps/the_monkeys/src/app/discussions/sitemap.ts apps/the_monkeys/src/app/sitemap.ts apps/the_monkeys/src/app/robots.ts apps/the_monkeys/src/app/llms.txt/route.ts apps/the_monkeys/src/lib/discussionSeo.ts apps/the_monkeys/__tests__/src/lib/discussionSeo.test.ts
git commit -m "feat: publish discussion pages to search and AI crawlers"
```

---

## Self-review

- Mobile: feed and thread use a single column, 16px body text, 44px controls, and `pb-24` so the five-tab bar does not cover the last item. The drawer gains Discussions because it reads `DISCOVER_ITEMS`. The bottom bar stays five tabs.
- SEO: each public thread has its own title, description, canonical URL, Open Graph article metadata, and `DiscussionForumPosting` JSON-LD in the server HTML. Member-only threads use `noIndexRobots`. Missing threads call `notFound()`.
- GEO and AI fetch: the body is in the first HTML response, `llms.txt` links the collection, and `/discussions/sitemap.xml` lists public ids only.
- Speed: the first screen does not wait on a client fetch. Client components are the composer, actions, reply form, load-more, and the group tab.
- DRY: one card, one composer, one canonical thread URL for site and group posts.
- Copy: no em dash. SEO text goes through `normalizeSeoText`.
- Out of scope: image upload, reply notifications, a sixth mobile tab, and the blog editor.
