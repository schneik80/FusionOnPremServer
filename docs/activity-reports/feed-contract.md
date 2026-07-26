> **HISTORICAL REFERENCE (dead approach).** This documents the Fusion Team notifications feed, which turned out to be first-party-gated: the endpoint returns HTTP 500 for third-party APS app tokens, and nothing in the product uses it. Kept only as a record of the investigation. See [STATUS.md](STATUS.md).

# Fusion Team Notifications feed — engineering reference

> Undocumented first-party APS endpoint the Fusion Team web client uses for its activity feed.
> Captured & verified against hub `imallc` on 2026-06-23. Not in public APS docs — treat as
> **may change without notice**; the parser must degrade gracefully and we keep a DOM-scrape fallback.

## Endpoint

```
GET https://developer.api.autodesk.com/fusionteam/notifications/v2/hubs/{hubId}/feeds/network/@me
    ?count=40&start={offset}&page={n}
Authorization: Bearer <APS access token>      # standard APS token; data:read is sufficient
X-Ads-Region: <US|EMEA|AUS>                    # set when configured (same as the GraphQL client)
```

- `{hubId}` is the short hub slug (e.g. `imallc`), **not** the `a.`-prefixed DM id. The DM id is
  available inside each object as `hub.forgeId` (e.g. `a.YnVzaW5lc3M6aW1hbGxj`).
- Pagination: `start = (page-1) * count`. Page 1 = `?count=40&start=0&page=1`.

## Response envelope

```jsonc
{
  "startIndex": "40", "count": "40", "totalObjects": "46", "endIndex": "0",
  "objects": [ /* feed objects, newest first */ ],
  "links": { "link": { "rel": "nextPage", "href": ".../@me?count=40&start=80&page=3" } }
}
```

Loop pages until there is **no** `links.link` with `rel:"nextPage"` (or `objects` is empty).
`totalObjects` is the upstream total. Note many numeric fields are JSON **strings** ("40", "2").

## Object types (`@type` / `type`)

| `@type` | `type` | Meaning |
|---|---|---|
| `wipDioWidgetObject` | `DATA` | a file/design version event (the bulk) |
| `activityFeedDataObject` | `COMMUNITY` | lifecycle event (e.g. *project created*); description in `title.content` (HTML) |

## Field → normalized model (per DATA object)

| Model field | JSON path | Notes |
|---|---|---|
| hub id / name | `hub.hubId`, `hub.name` | |
| hub DM id | `hub.forgeId` | bridges feed ↔ Manufacturing Data Model GraphQL / DM API |
| project | `publishedTo.{id, publishedToName, publishedToUrl}` | `type:"Group"` |
| folder | `parentFolderUrn` | `urn:adsk.wipprod:fs.folder:co.…` |
| design id | `id` / `permalinkId` | `DT…QT…` lineage+version key |
| design lineage / tip | `lineageUrn`, `tipVersionUrn` | match DM/GraphQL ids exactly |
| design name / type | `displayTitle` / `fileName`, `fileType` | f3d / f2d / iam / pdf / json |
| created | `creationTime` | epoch **ms** (string) |
| last change | `lastModified` / `changeTime` / `lastActivity.time` | epoch ms, absolute |
| version count (tip) | `version` | string int |
| last actor | `lastActivity.{accountId, displayName}` | differs from `owner` → multi-contributor resolvable |
| owner | `owner.{accountId, displayName, userId(email)}` | |
| views / social | `views.{views, viewers}`, `postCount`, `likeCount` | bonus signals |
| drill-down | `links.link[]` rels: `versions`, `comment`, `self`, `permalink` | enrichment endpoints |

For COMMUNITY objects: `displayTitle`/`title.content` is HTML; `publishedTo` is the project; parse the
action from the text (e.g. "has created … project").

## Action inference (no explicit verb in JSON)

- `version == 1` AND `creationTime ≈ changeTime` → `created` / `uploaded`
- `version > 1` → `updated`
- COMMUNITY → parse from `title.content` (created / shared / commented / …)

## Enrichment (already implemented in this repo — see plan §reuse)

- Full contributor history per design → `api.GetItemDetails` (itemVersions; each version has an author).
- Uses / where-used / drawings → `api.GetWhereUsed` / `GetOccurrences` / `GetDrawingsForDesign`.
- Milestone count → Manufacturing Data Model GraphQL (Phase 5 spike).
- Comment text → the object's `links rel=comment` endpoint when `postCount > 0` (Phase 5 spike).

## Id-namespace caveat (important)

The feed's hierarchy ids are **not** the GraphQL/Manufacturing-Data-Model ids the browser nav uses:

- project → `publishedTo.id` is the **portal group id** (e.g. `20240903798465131`), not the DM/GraphQL project id.
- folder → `parentFolderUrn` (`urn:adsk.wipprod:fs.folder:co.…`).
- design → `permalinkId` (`DT…QT…`) / `lineageUrn`, not the GraphQL `item.id`.

So you cannot scope an activity report by passing a GraphQL nav id. The dashboard sidesteps this by
**drilling with the feed's own child ids**: a hub report's `children[]` carry feed-native project ids,
a project report's children carry folder URNs, etc. `BuildReport`'s `inScope` matches those exact
values. Only the **hub slug** crosses over (derived from the GraphQL hub's AltID/WebURL via `HubSlug`).

## Security

The feed body contains **no token**, but never commit access tokens, cookies, or the `Authorization`
header into fixtures or docs. Scrub before saving sample responses.
