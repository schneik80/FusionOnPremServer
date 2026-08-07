// Screenshot capture over the Fusion palette bridge. The palette page cannot
// read the filesystem, and the Python side holds no session cookie — so the
// add-in captures the (downscaled) viewport, base64s it, and hands it over in
// chunks via fusionSendData's synchronous returnData channel:
//
//   captureScreenshot {}            → {ok, id, chunks, name}
//   getScreenshotChunk {id, index}  → one base64 chunk (raw string)
//
// Chunked request/response (rather than one giant payload or sendInfoToHTML
// events) because the bridge's payload ceiling is undocumented — 256 KiB
// chunks stay far below any plausible limit, and a lost reply just fails the
// whole capture cleanly.

export function hasFusionBridge(): boolean {
  return typeof window.adsk?.fusionSendData === 'function'
}

interface CaptureMeta {
  ok: boolean
  id: string
  chunks: number
  name: string
}

// captureFusionScreenshot asks the add-in for a viewport screenshot and
// reassembles it into a File. Returns null when there is no bridge, the
// capture failed Fusion-side, or a chunk went missing.
export async function captureFusionScreenshot(): Promise<File | null> {
  if (!hasFusionBridge()) return null
  try {
    const raw = await window.adsk!.fusionSendData('captureScreenshot', '{}')
    const meta = JSON.parse(raw) as CaptureMeta
    if (!meta.ok || !meta.id || meta.chunks <= 0) return null
    let b64 = ''
    for (let i = 0; i < meta.chunks; i++) {
      const chunk = await window.adsk!.fusionSendData(
        'getScreenshotChunk',
        JSON.stringify({ id: meta.id, index: i }),
      )
      if (!chunk) return null
      b64 += chunk
    }
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0))
    return new File([bytes], meta.name || 'screenshot.png', { type: 'image/png' })
  } catch {
    return null
  }
}
