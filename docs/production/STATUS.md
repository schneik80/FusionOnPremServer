# Production — status

A light **MES / product planning & tracking** app — one of the five project apps,
beside Chat, Tasks, Wiki and Whiteboards.

**The problem it solves.** Paper "travellers" — the order-specific packet that
follows a job through the shop — have one hard failure mode: *which document
version did this run actually use?* Copies circulate, revisions land mid-run, and
the record of what was on the machine is lost. Production answers that by making
every document reference a **version pin** and every run an **immutable freeze**
of the plan it started from.

## Model

| Concept | What it is |
|---|---|
| **Job** | The *as-planned* process: a DAG of Steps. `j<n>` per project. |
| **Step** | A node with a persisted canvas position. Kind **`step`** carries version-pinned **plan documents** and **placeholder** slots for documents supplied per run; kind **`decision`** carries **results** instead and carries no documents at all. Kind is set at create time and never patched. |
| **Result** | One named, colour-coded outcome of a decision — "Pass", "Rework". `dr<n>`. Each result is the source of its own outgoing edges, so the result list *is* the branch structure. |
| **Edge** | A directed Step→Step link. Stored as a flat list on the Job; self-loops, duplicates and cycles are rejected. An edge out of a decision must name the result it branches on (`fromResultId`); an edge out of a plain step must not. |
| **Batch** | A dated *as-run* instance. On creation it **freezes** the plan. `b<n>` per job. |
| **Fulfillment** | A version-pinned document supplied into a batch — filling a placeholder, or an extra **as-run artifact** (e.g. on-machine-modified NC code). |
| **Refs** | `fls:task` / `fls:doc` tokens on a batch — related tasks and wiki/hub documents, rendered as live cards. |
| **Hidden steps** | Frozen step ids the run view collapses — typically the branch a decision did not take. Presentation metadata on the `Batch`, *never* a field on the frozen step. |

### Decisions and branching

A plain chain of steps can't express a traveller that forks — inspect, then
pass or rework or scrap. A **decision** is the second node kind:

- Its **results** are the branches. Each gets its own out-port on the canvas
  and its own colour, which strokes the edge leaving it.
- **`Color` is a palette token, not a hex value** (`green`/`amber`/`red`/
  `blue`/`violet`/`teal`/`grey`). A closed enum cannot inject CSS through the
  `sx` rule or SVG `stroke` it lands in, and a user-picked hex has no way to
  stay legible in both light and dark mode. The client resolves each token per
  theme (`web/src/production/resultcolors.ts`).
- **Deleting a result cascades to its edges.** An orphan would be worse than
  broken: the canvas can't draw an edge with no port, but it still counts for
  cycle detection and the duplicate check — so the user would be told a new
  edge "would create a cycle" by one they can neither see nor delete.
- **Cycle detection is unchanged.** Every result leaves the same node, so the
  digraph is identical whether or not edges are result-bound. Resist making
  `reaches` branch-aware: "these branches are exclusive so the cycle is fine"
  is a runtime-path argument, and this is a static plan graph.
- The duplicate key is `(from, fromResultId, to)`, so two results may
  legitimately converge on one step.
- A result id must belong to the step it is used from. Child ids come from the
  job-wide counter and are unique across the job, so a result id copied from a
  *different* decision would otherwise resolve.

### The two invariants

1. **A pin is exact.** `DocSnapshot` stores `versionId` (the DM version urn),
   `versionNumber`, and `rootComponentVersionId` (that version's thumbnail cvId).
   The client sends only a document reference; the server resolves the pin
   (`api.SnapshotDocVersion`) so a version can never be forged. The human version
   number is parsed from the version urn's `?version=N`, which is authoritative
   for *every* item kind — the MDM GraphQL details call is best-effort decoration
   for design thumbnails only (plain files carry no `tipVersion` there, and
   DM-created items may not have propagated to the MDM graph at all).
2. **A run is immutable.** `CreateBatch` deep-copies each step's identity, kind,
   pinned documents and placeholders into `Batch.Steps`. The batch UI renders —
   and `AddFulfillment` validates — against that frozen copy, never the live graph.
   Deleting a step or adding a placeholder for the next run cannot alter, hide, or
   retroactively re-score a batch that already happened.

   **`Batch.HiddenSteps` does not weaken this.** It is a mutable string list on
   the `Batch`, modelled on `Refs` — deliberately *not* a flag inside
   `BatchStep`, whose entire purpose is to be a pure record. A mutable field in
   there would make "frozen" mean "frozen except these", and every future reader
   would have to know which. Nothing derived from a run reads the hidden list:
   completeness, fulfillment validation and **Where-Used** all still walk every
   frozen step, because a document *was* used in that run whether or not the UI
   collapses its row.

   **Deep-copy discipline.** `DecisionResult` is all value types, so
   `append([]DecisionResult(nil), …)` is a sufficient copy — but the copy must
   exist at every site (`copyStep`, `copyBatch`, and the `CreateBatch` freeze).
   The site that bites hardest is not the freeze but the *rollback*:
   `mutateJob` snapshots via `copyJob`, and `UpdateResult` writes through a
   `*DecisionResult` into the live slice, so an aliased snapshot would restore
   the very edit it was meant to undo. `TestBatchFreezeImmutability` covers the
   in-place case explicitly for exactly this reason.

## Schema versions

| Version | What changed |
|---|---|
| 1 | Original shape. |
| 2 | Schema provenance stamp joins the envelope (loader backfills it). |
| 3 | Decision steps (`Step.Kind`, `Step.Results`), result-bound edges (`Edge.FromResultID`), frozen `BatchStep.Kind`, and `Batch.HiddenSteps`. |

**The v2→v3 migration is a deliberate no-op.** Every field v3 adds has a zero
value that already means what a v2 file meant: `""` kind reads as `"step"`, a
nil result list is no results, an empty `FromResultID` is a plain edge, nothing
hidden. There is nothing to rewrite.

The version still had to move, for **downgrade protection**. `migrate.Apply`
never persists what it migrates (`internal/migrate/migrate.go`), so a v2 file
stays v2 on disk until something saves it — but an *older* binary reading a v3
file would decode it into its own structs, silently drop the new fields, and
erase them for every job in the project on its next save. At `fileVersion = 3`
that binary hits `ErrFutureVersion` and refuses the file instead.

Because the migration doesn't stamp anything, `Kind == ""` is normalised to
`"step"` in exactly two places — `prodStepDTO` and the `prodBatchDTO` step loop
in `server/dto_production.go`. That is the one point where the migrated read
path and the three registry-**bypassing** raw scanners (`Store.Mine`,
`Store.ListForProjects`, `docrefs.FindDocRefs`, which read files straight off
disk) converge, so both see the same value. Compare kinds through
`production.IsDecision` / `prodStepKind`, never against the raw field.

**Backup needs no changes for a version bump.** `server/handlers_backup.go`
already reports `production.CurrentVersion()`, and verify/restore only refuse
versions *newer* than the running build — older snapshots still restore and
migrate on load.

## Layout

**Backend**
- `production/types.go`, `production/store.go` — one `production.json` per project
  under `<config>/production/<sanitized-projectId>/`. Copies the `tasks/` store
  posture: atomic temp+rename writes, per-project mutex, `.bak` on corruption,
  future-version guard, clone + rollback on save failure. Mutations copy the
  returned object **under the lock**.
- `api/production_snapshot.go` — version-pin resolution (+ `versionBelongsToItem`,
  so an upload may assert the version it just created but never a foreign one).
- `server/handlers_production.go`, `server/dto_production.go`, routes in
  `server/routes.go`. Authorization reuses `chat.Authorizer`: `CapRead` to view,
  `CapPost` to edit, `CapModerate`-or-creator to delete a job/batch.

**Frontend** — `web/src/production/`
- `ProductionApp` → project tab; master/detail over `JobList` + `JobDetail`.
- `JobDetail` has three views: **Flow** (`JobCanvas` — pan/zoom SVG canvas lifted
  from `RelationGraph`, draggable persisted step positions, drag-from-port to
  connect), **List**, and **Batches**. Its header carries **Duplicate** beside
  Delete.
- `ResultsEditor` / `resultcolors.ts` — the decision outcome table (label +
  swatch + delete), shared by the List card and the canvas drawer so the two
  can't drift.

### Canvas geometry

A plain step is a fixed 176×74 card. A **decision** is a rounded-diamond head
(208×60) fused to a rounded-rect strip of 22px result rows, and its height is
therefore a function of its result count.

It is *not* one big rhombus, and that was a deliberate call. A rhombus offers
usable width `W·(1−|2y/H−1|)` at height `y`, so a four-result list would get
rows a third as wide at the top and bottom as in the middle — either every
label truncated to the narrowest row, or colored underlines of visibly
different lengths — and each result's port would sit at a different `x`,
fanning the edge curves out unevenly from one node. Split, every row is the
same width and every port sits on one vertical edge. The rhombus itself is an
inline SVG **background** (`roundedDiamondPath`) with the title as an HTML
overlay, so the node keeps MUI theming, its `Tooltip` and `noWrap` ellipsis.

Two performance constraints hold the design:

- **Node size is closed-form** (`nodeH`, `resultPortY`), never measured.
  Measuring would mean a DOM read per node per frame and a
  render→measure→re-render loop, which is what the `memo`/`liveRef` design
  exists to avoid.
- **The port table is built in the pass that already indexes steps by id**, so
  edge routing stays two map lookups per edge and keeps its O(N+E) shape at the
  caps (400 edges × 200 steps, re-run on every mousemove render). Edge stroke
  colour is resolved in that memo too, not in the JSX.

Canvas interactions: **double-click a node to rename it in place**, and **`+`
chains from the selection** — the new node lands to the right with its in-port
aligned to the port it leaves from, and the edge is drawn for you (off a
decision, it binds to the first result with no edge yet). A palette toggle
picks whether `+` creates a step or a decision.
- `BatchesView` / `BatchDetail` / `BatchTimeline` — prove vs production lanes on a
  time axis (rust-orange `#b7410e`, the History graph's share-lane hue), per-step
  frozen documents, placeholder fulfillment, as-run artifacts, completeness bar.
- `DocSourceButton` supplies a document from **the hub** or **an upload**;
  `PinnedDocChip` renders a pin with its exact version badge and jumps to the
  document via `useGoToDocument`.
- `ProductionScreen` — the cross-project rail screen (`app=production`): runs in
  flight across every project, and jobs you own.

### Where uploads land

Production files its own uploads rather than dropping them in the project root:

```
<project>/Jobs/<job name>/                  plan documents (step editor)
<project>/Jobs/<job name>/<batch name>/     fulfillments + as-run artifacts
```

Each level is created on demand and **reused if it already exists**
(`api.EnsureFolderPath` → `ensureSubfolder`, which matches case-insensitively,
so a hand-made `jobs` folder is adopted rather than duplicated). Job and batch
names are free text, so `folderSafe()` strips the characters Windows and the DM
API reject before they become folder names.

This is opt-in per upload: `POST /api/uploads` takes `ensureFolders=true`.
Browsing uploads leave it unset and keep the must-exist behaviour, where a
missing folder means the client sent a stale path and inventing one would hide
the mistake.

**Cross-linking** — `components/productioncard/`
- `fls:job` / `fls:batch` tokens (`prodref.ts`) unfurl into a `ProductionCard`
  wherever tokens render (chat, wiki, task bodies), opening a read-only
  `ProductionViewDialog`. `ProductionRefDialog` inserts them from the chat
  composer, the wiki toolbar, and task details.

## API

All IDs are query params. `projectId` is the project URN; `jobId`/`batchId` are
per-scope ids.

```
GET    /api/production/jobs          ?projectId                     list + capabilities
POST   /api/production/jobs          ?projectId                     {hubId,projectName,name,description}
POST   /api/production/jobs/duplicate ?projectId&jobId              {hubId,projectName} — copies the plan, not the runs
PATCH  /api/production/jobs          ?projectId&jobId
DELETE /api/production/jobs          ?projectId&jobId               moderator or creator
GET    /api/production/job           ?projectId&jobId               one job, full graph
GET    /api/production/mine                                         cross-project (no roster check)

POST   /api/production/steps         ?projectId&jobId               {kind,title,description,x,y}
PATCH  /api/production/steps         ?projectId&jobId&stepId        x,y must be sent together; kind is NOT patchable
DELETE /api/production/steps         ?projectId&jobId&stepId        also drops incident edges

POST   /api/production/edges         ?projectId&jobId               {from,fromResultId,to} — DAG enforced
DELETE /api/production/edges         ?projectId&jobId&edgeId

POST   /api/production/results       ?projectId&jobId&stepId        {label,color} — decision steps only
PATCH  /api/production/results       ?projectId&jobId&stepId&resultId
DELETE /api/production/results       ?projectId&jobId&stepId&resultId   cascades to that result's edges

POST   /api/production/placeholders  ?projectId&jobId&stepId
PATCH  /api/production/placeholders  ?projectId&jobId&stepId&placeholderId
DELETE /api/production/placeholders  ?projectId&jobId&stepId&placeholderId

POST   /api/production/plandocs      ?projectId&jobId&stepId        {hubId,itemId,dmProjectId,name,kind}
DELETE /api/production/plandocs      ?projectId&jobId&stepId&planDocId

POST   /api/production/batches       ?projectId&jobId               freezes the plan
GET    /api/production/batch         ?projectId&jobId&batchId
PATCH  /api/production/batches       ?projectId&jobId&batchId
DELETE /api/production/batches       ?projectId&jobId&batchId       moderator or creator

POST   /api/production/fulfillments  ?projectId&jobId&batchId       {stepId,placeholderId,…doc,source,isAsRun}
DELETE /api/production/fulfillments  ?projectId&jobId&batchId&fulfillmentId

POST   /api/production/batchrefs     ?projectId&jobId&batchId       {token} — fls:task | fls:doc
DELETE /api/production/batchrefs     ?projectId&jobId&batchId&token

POST   /api/production/batchhidden   ?projectId&jobId&batchId       {stepId} — collapse in the run view
DELETE /api/production/batchhidden   ?projectId&jobId&batchId&stepId
```

Job/step/edge/**result**/placeholder/plandoc mutations return the **whole updated
job** (it drops straight into the `['prodJob', …]` cache); batch/fulfillment/ref/
hidden mutations return the **affected batch**. Results return the whole job
because a delete cascades to edges — the client needs the graph, not the step.

`POST /jobs/duplicate` copies steps, edges, results, placeholders and pinned
plan documents into a new job. It does **not** copy batches, and it preserves
every child id and counter verbatim — see *Duplicating a job* below.

## Duplicating a job

`DuplicateJob` runs in **one** `mutate` closure — reading the source and
writing the copy together, so a concurrent delete can't race between them and
one logical copy is one file write.

- **Re-ids only the job**: `ID`/`Num` from `NextJobNum`, `CreatedBy` the
  duplicator, timestamps now.
- **Preserves every child id verbatim** (`Step.ID`, `Edge.ID`/`From`/`To`/
  `FromResultID`, `PlanDoc.ID`, `Placeholder.ID`, `DecisionResult.ID`) **and the
  counters**. Child ids are job-scoped, so there is no uniqueness reason to
  renumber, and renumbering would mean remapping six fields correctly.
  Resetting `NextChildNum` to 1 is the trap: it would mint a second `e1` into a
  job that already has one, and `DeleteEdge` matches by id — the user would
  delete the wrong edge.
- **Copies no batches** (`NextBatchNum: 1`). A run belongs to the job it ran
  under; copying one would forge its provenance and double every run-level hit
  in the Where-Used scan, so a drawing would report "used in run B-12" twice
  under two different jobs.
- **Pins stay byte-identical.** Re-resolving to tip would silently upgrade
  them and defeat `DocSnapshot`; `AddedBy`/`AddedAt` are preserved too, since
  the plan contents are a copy of a historical record even though the job
  envelope gets a new author.
- The name is trimmed **by runes** before `" (copy)"` is appended, so a job
  already at the 200-rune cap is still duplicable.

Client-side, `duplicateJob` is the one job mutation that **awaits** its list
invalidation: the caller selects the copy on success, and `ProductionApp`'s
recovery effect resets the selection to `jobs[0]` whenever the selected id
isn't in the list yet.

## Shipped

- P1 store + CRUD, P2 flow canvas, P3 plan documents + batches + version pinning,
  P4 upload-to-fulfill + timeline + completeness, P5 cross-project screen.
- A `/code-review` pass at high effort; all 10 reported findings fixed (notably a
  copy-outside-the-lock data race in `CreateJob`/`UpdateJob`, the live-plan batch
  record, tip-following pins on uploads, and `v0` on plain files), plus the
  follow-up sweep: job-scoped rollback, summary list DTO, canvas memoization,
  capability probes that no longer swallow errors into a silent read-only tab,
  and shared `ToolBtn` / `StepNumBadge` / `PlaceholderChip`.
- **P6 — decisions and editing ergonomics** (schema v3): duplicate a job; a run
  date on batch creation (via the new shared `components/DateField.tsx`);
  double-click rename and chain-from-selection on the canvas; decision nodes
  with colour-coded results, result-bound edges and a node palette; hidden
  steps in the run record.

## Performance notes

Three things are deliberately shaped for the interaction pattern (a canvas drag
PATCHes on every node release; text saves on every blur):

- **Rollback is job-scoped.** `mutateJob` snapshots only the job being written,
  not the whole project file. The whole-file clone remains only for create/delete
  job, which are rare.
- **The jobs list ships counts, not graphs.** `GET /api/production/jobs` returns
  `ProdJobSummaryDTO` (step/batch/active counts); the selected job's full graph
  comes from `GET /api/production/job`. Previously both polled the same full
  payload every 15s.
- **The canvas memoizes.** `StepNode` is `memo`'d with identity-stable handlers
  (they read live state through a ref), and edge geometry is a `useMemo` over a
  step `Map` — so a pan or drag re-renders the moving node, not all of them.

## Known gaps / next

- **A batch freezes steps but no edges, and records no taken branch.** That is
  why `BatchStep` freezes `Kind` but deliberately *not* `Results`: frozen
  results would name branches the record cannot express. The run view is a flat
  list, and hiding the untaken branch is manual. Doing this properly means
  freezing `Edges []Edge` on the `Batch` alongside `Steps`, after which
  "which result did this run take?" becomes storable *and* derivable —
  `HiddenSteps` would then be a cache of a reachability computation rather than
  the only record. Don't add `TakenResults` without the frozen edges: it would
  store the consequence and discard the cause.
- A decision's results can't be reordered; they render in creation order.
- Pins always freeze the **tip**; pinning an arbitrary historical version is not
  exposed.
- `SnapshotDocVersion` does two independent lookups with no cross-check — a save
  landing between them can pin a version whose thumbnail cvId is skewed. The
  number itself is safe (it comes from the pinned urn), and a mismatched details
  tip is discarded rather than borrowed.
- The cross-project screen (`/mine`) skips per-project roster checks by design;
  a user removed from a project keeps seeing their old jobs there until deleted.

## Verifying

```
go build ./... && go test ./...        # freeze immutability (incl. in-place edits), DAG cycles,
                                       # decision/edge invariants, result cascade, duplicate,
                                       # hidden steps, v1→v3 and v2→v3 migration, Mine, corruption
cd web && npx tsc --noEmit && npm run lint:i18n && npm test && npm run build
make run                               # needs APS credentials; HTTPS + login
```

End-to-end: create a job → add and connect steps on the canvas → attach a plan
document → add a placeholder → create a batch → supply the placeholder (browse
and upload) → add an as-run artifact → **publish a new version of the plan
document upstream and confirm the batch still shows the old pinned version while
the plan shows the new tip.**

For the P6 surfaces:

1. With a step selected, press **+** — the new node lands to its right, already
   connected. Double-click a node, rename, press Escape (unchanged) then Enter
   (saved).
2. Switch the palette to **Decision**, add one, give it *Pass* (green) and
   *Fail* (red). Confirm the diamond head, the result strip, the coloured
   underlines and one port per result; drag each port to a different step and
   confirm the edges take the result colours.
3. Delete a result — its edge must vanish with it, and adding a new edge must
   not report a phantom cycle.
4. Create a batch with a run date **in the past**; confirm the timeline places
   it correctly.
5. Hide two steps in the run, reload, confirm they stay hidden and the header
   toggle reveals them. Confirm the hidden step's document still appears under
   the document's **Where-Used** tab.
6. **Duplicate the job.** The copy must carry every step, edge, result and pin
   at its original version, and **no batches**.
7. Restart the server and confirm `production.json` reads `"version": 3` with a
   `.v2.bak` beside it; then run Settings → Backups → **Backup now → Verify**
   and a **Restore**.
