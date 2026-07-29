// Production refs are the fls:doc / fls:task siblings (see doccard/docref.ts):
// compact, text-safe tokens that let wiki markdown, chat messages, and task
// bodies reference a production Job or Batch. The stored content carries only
// a small pseudo-URL token; renderers swap it for a rich ProductionCard at
// display time. In the wiki the token travels inside a normal markdown link;
// in chat it sits inline in the plain-text body.

export interface JobRef {
  hubId: string // GraphQL hub id
  projectId: string // project urn (the production store's key)
  projectName: string // display, at insert time
  jobId: string // per-project job id, "j<num>"
  jobName: string // display, at insert time
}

export interface BatchRef extends JobRef {
  batchId: string // per-job batch id, "b<num>"
  batchName: string // display, at insert time
}

export const JOB_REF_PREFIX = 'fls:job?'
export const BATCH_REF_PREFIX = 'fls:batch?'

export function encodeJobRef(ref: JobRef): string {
  const sp = new URLSearchParams()
  sp.set('hubId', ref.hubId)
  sp.set('projectId', ref.projectId)
  sp.set('projectName', ref.projectName)
  sp.set('jobId', ref.jobId)
  sp.set('jobName', ref.jobName)
  return JOB_REF_PREFIX + sp.toString()
}

export function encodeBatchRef(ref: BatchRef): string {
  const sp = new URLSearchParams()
  sp.set('hubId', ref.hubId)
  sp.set('projectId', ref.projectId)
  sp.set('projectName', ref.projectName)
  sp.set('jobId', ref.jobId)
  sp.set('jobName', ref.jobName)
  sp.set('batchId', ref.batchId)
  sp.set('batchName', ref.batchName)
  return BATCH_REF_PREFIX + sp.toString()
}

export function parseJobRef(url: string): JobRef | null {
  if (!url.startsWith(JOB_REF_PREFIX)) return null
  const sp = new URLSearchParams(url.slice(JOB_REF_PREFIX.length))
  const projectId = sp.get('projectId') ?? ''
  const jobId = sp.get('jobId') ?? ''
  if (!projectId || !jobId) return null
  return {
    hubId: sp.get('hubId') ?? '',
    projectId,
    projectName: sp.get('projectName') || 'project',
    jobId,
    jobName: sp.get('jobName') || 'job',
  }
}

export function parseBatchRef(url: string): BatchRef | null {
  if (!url.startsWith(BATCH_REF_PREFIX)) return null
  const sp = new URLSearchParams(url.slice(BATCH_REF_PREFIX.length))
  const projectId = sp.get('projectId') ?? ''
  const jobId = sp.get('jobId') ?? ''
  const batchId = sp.get('batchId') ?? ''
  if (!projectId || !jobId || !batchId) return null
  return {
    hubId: sp.get('hubId') ?? '',
    projectId,
    projectName: sp.get('projectName') || 'project',
    jobId,
    jobName: sp.get('jobName') || 'job',
    batchId,
    batchName: sp.get('batchName') || 'batch',
  }
}

// jobRefFromJob / batchRefFromBatch are the taskRefFromTask siblings: a job
// already carries its hub + project, so anything holding one can address it
// without re-deriving the context. A batch is addressed relative to its job.
export function jobRefFromJob(j: {
  hubId: string
  projectId: string
  projectName: string
  id: string
  name: string
}): JobRef {
  return {
    hubId: j.hubId,
    projectId: j.projectId,
    projectName: j.projectName,
    jobId: j.id,
    jobName: j.name,
  }
}

export function batchRefFromBatch(job: JobRef, b: { id: string; name: string }): BatchRef {
  return { ...job, batchId: b.id, batchName: b.name }
}

// Markdown forms for the wiki: a link whose href is the token, degrading to a
// plain named link in any other markdown renderer.
export function jobRefMarkdown(ref: JobRef): string {
  return `[${ref.jobName.replace(/[[\]]/g, '')}](${encodeJobRef(ref)})`
}
export function batchRefMarkdown(ref: BatchRef): string {
  return `[${ref.batchName.replace(/[[\]]/g, '')}](${encodeBatchRef(ref)})`
}
