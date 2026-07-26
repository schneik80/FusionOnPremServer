# Wiki — status

A per-project markdown wiki. Unlike every other project app, the published
data is **not a local store**: pages are plain `.md` files in a project-root
**"Wiki" folder in Fusion Team**, written and read through the APS Data
Management API. The wiki therefore travels with the project — visible in
Fusion Team itself, governed by the project's own permissions, and versioned
by DM like any other file.

## The two-tier model

| Tier | Where | What |
|---|---|---|
| **Drafts** | The browser — IndexedDB (`fls-wiki` DB), per project | Authored locally; never leave the device until published. Autosaved on a 600 ms debounce. |
| **Pages** | Fusion Team — `<project root>/Wiki/<slug>.md` | Published markdown; each publish is a new DM **version** of the same item lineage. |

A draft records the page lineage it is linked to (`baseItemId`) and the tip
version it was based on (`baseVersion`). The sidebar merges both tiers into
one list and derives each entry's status by comparing that base against the
live tip: `draft` (never published), `published` (clean), `modified` (local
edits), `behind` (remote moved ahead, local clean), `conflict` (both changed).

### Stale-overwrite protection (the 409)

`PublishWikiPage` (`api/wiki_publish.go`) enforces two conflict checks unless
`force` is set, and `handleWikiPublish` maps `ErrWikiConflict` to **HTTP 409**:

- **Linked draft:** if the item's live tip no longer equals the draft's
  `baseVersion`, the page moved upstream since the draft was opened — refuse.
- **New page:** if a same-named `.md` already exists in the Wiki folder (e.g.
  published from another device), don't silently fork it — refuse.

On a 409 the client (`WikiApp.handlePublish`) asks the user to confirm and
retries with `force: true`, which overwrites (linked) or adopts the existing
item (new page). The `behind`/`conflict` banners offer the inverse: pull the
live tip into the draft ("update" / "take theirs").

## Backend

- `api/wiki.go` — read side, all in Data-Management id space: the GraphQL hub
  id is translated once to the DM hub id
  (`GetHubDataManagementID` via `alternativeIdentifiers`), the project is
  addressed by its DM id (altId). Listing walks project topFolders → the
  `Wiki` folder (case-insensitive) → its `.md` items (data/v1, paginated at
  100/page); an absent Wiki folder is simply "no pages". Download follows
  item tip → version storage object → **signed S3 download** (capped 32 MiB).
- `api/wiki_publish.go` — write side. The Data Management upload sequence for
  any file: **create a storage object → push bytes to OSS via a signed S3
  upload (sign → PUT → finalize) → create the item** (first publish, type
  `items:autodesk.core:File`) **or a new version** (`POST projects/{pid}/versions`
  — there is no items/{id}/versions endpoint). Folders — the project-root
  `Wiki`, a per-page `<slug>`, and its `images` — are created on demand and
  reused case-insensitively. Needs the `data:write` + `data:create` scopes.
  Rename PATCHes the item's `displayName` (the lineage id — and therefore
  links, versions, and a linked draft's base — survives) and best-effort
  renames the page's images subfolder to match.
- `server/handlers_wiki.go` + routes in `server/routes.go`. There is **no
  chat-authorizer layer here**: every call runs on the caller's own APS
  token, so Fusion Team's project permissions are the authorization. Publish,
  rename and image upload verify the request's `hubId` against the session
  hub (`hubMatches` — form/body hub ids are checked in the handler, query
  params centrally in `requireHub`; see [hubs](../hubs/STATUS.md)).

## Frontend — `web/src/wiki/`

- `WikiApp` — the project tab (`{ active }` contract, published-pages fetch
  gated on the tab being open). Left: sidebar merging pages + drafts; right:
  reader or the split-pane editor. Handles publish (incl. the 409 confirm),
  rename, pull-to-draft, and conflict reconciliation.
- `draftStore.ts` / `useDrafts.ts` — the IndexedDB draft store (composite key
  `${projectId}::${pageKey}`, indexed by project) behind a thin promise
  wrapper; `slugify` derives the `.md` filename from the title.
- `WikiEditor` — CodeMirror 6 markdown editor with a toolbar: formatting,
  links, images (upload, hub browse, or URL via `ImageUrlDialog`), and card
  tokens — `fls:doc` (hub documents), `fls:task` (attach or quick-create),
  `fls:job`/`fls:batch` (production) — inserted as markdown links so they
  degrade to plain named links in any other renderer.
- `Markdown.tsx` — react-markdown + remark-gfm (tables, task lists) +
  rehype-highlight (fenced code) + rehype-slug (heading anchors); card tokens
  unfurl to `DocumentCard`/`TaskCard`/`ProductionCard`; prose styled via `sx`,
  no stylesheet.
- `MarkdownView.tsx` — the reader: scroll container plus a sticky "On this
  page" TOC (H1–H3, scroll-spy) once a page has ≥ 3 headings.

### Images

`POST /api/wiki/image` (multipart, ≤ 32 MiB) stores an image under
`Wiki/<slug>/images/` and returns its lineage urn; re-uploading a same-named
image adds a version rather than a duplicate. Pages embed it as
`/api/wiki/image?dmProjectId=…&itemId=…` — a same-origin subresource carrying
the session cookie, streamed with the content type S3 reports and
`Cache-Control: private, max-age=3600`.

## API

`hubId` is the GraphQL hub id (translated server-side); `dmProjectId` is the
project's Data-Management id (altId); `itemId` is the DM item lineage urn.

```
GET  /api/wiki/pages    ?hubId&dmProjectId                    list published pages
GET  /api/wiki/page     ?dmProjectId&itemId                   one page's markdown
POST /api/wiki/publish  {hubId,dmProjectId,itemId?,slug,markdown,baseVersion?,force?}
                                                              409 on stale/name conflict
POST /api/wiki/rename   {hubId,dmProjectId,itemId,oldSlug?,newSlug}
POST /api/wiki/image    multipart: hubId,dmProjectId,slug,file   → {itemId,name}
GET  /api/wiki/image    ?dmProjectId&itemId                   stream tip bytes
```

Publish bodies are capped at 8 MiB, rename at 1 MiB, images at 32 MiB.

## Residual risks / known gaps

- **No page delete endpoint** — removing a page means deleting the file in
  Fusion Team; the app only lists what exists.
- **Drafts are device-local.** They are not synced between browsers and are
  **not in server [backups](../backup/STATUS.md)** (nothing server-side to
  back up; published pages live in Fusion Team, which is Autodesk's problem).
- Conflict detection is optimistic, not locking: two users can still race a
  `force` publish; DM versioning means nothing is lost, but the tip is
  last-writer-wins.
- Fenced-code highlighting ships the light `github.css` theme in both color
  modes (noted follow-up in `Markdown.tsx`).
- MDM GraphQL listings don't see DM-created content (the wiki image trees) —
  which is why the in-place hub browser uses `/api/browse/contents` (data/v1)
  for complete listings.
- Renaming only retitles the file; existing `fls:` tokens and image
  references keep working (they're by item id), but external links to the
  old name in Fusion Team's own UI are the user's to fix.

## Verifying

```
go test ./api/...                    # wiki + publish unit tests (offline)
cd web && npx tsc --noEmit && npm run build
```

End-to-end (needs APS login; upload sequence verified live against Fusion
Team): create a draft → publish (confirm `Wiki/<slug>.md` appears in Fusion
Team) → edit + republish (new version) → upload an image and embed it → edit
the same page from a second browser/device and confirm the first gets the 409
→ force-overwrite → rename and confirm the lineage (and draft link) survive.
