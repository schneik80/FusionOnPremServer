import { Box } from '@mui/material'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Document, Page, pdfjs } from 'react-pdf'
import 'react-pdf/dist/Page/AnnotationLayer.css'
import 'react-pdf/dist/Page/TextLayer.css'
import type { ViewerFile } from './kind'
import { FallbackViewer, ViewerSpinner } from './ui'

// pdf.js runs its parser in a web worker. Vite bundles the worker from the
// installed pdfjs-dist via new URL(...) so its version always matches react-pdf.
pdfjs.GlobalWorkerOptions.workerSrc = new URL(
  'pdfjs-dist/build/pdf.worker.min.mjs',
  import.meta.url,
).toString()

const MAX_PAGE_WIDTH = 900

// PdfViewer renders every page stacked, each sized to the pane width (capped so
// it doesn't blow up on a wide window). Pages stream via Range as pdf.js reads
// them. withCredentials carries the session cookie to the same-origin endpoint.
export function PdfViewer({ file }: { file: ViewerFile }) {
  const { t } = useTranslation('browse')
  const hostRef = useRef<HTMLDivElement | null>(null)
  const [numPages, setNumPages] = useState(0)
  const [width, setWidth] = useState<number>()
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    const el = hostRef.current
    if (!el) return
    const measure = () => setWidth(el.clientWidth)
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  // Memoize so react-pdf doesn't reload the document on every render.
  const source = useMemo(() => ({ url: file.url, withCredentials: true }), [file.url])

  if (failed) return <FallbackViewer file={file} reason={t('viewer.pdfFailed')} />

  return (
    <Box ref={hostRef}>
      <Document
        file={source}
        loading={<ViewerSpinner />}
        onLoadSuccess={(doc) => setNumPages(doc.numPages)}
        onLoadError={() => setFailed(true)}
        onSourceError={() => setFailed(true)}
      >
        {Array.from({ length: numPages }, (_, i) => (
          <Box key={i} sx={{ mb: 1.5, display: 'flex', justifyContent: 'center' }}>
            <Page
              pageNumber={i + 1}
              width={width ? Math.min(width, MAX_PAGE_WIDTH) : undefined}
              renderTextLayer
              renderAnnotationLayer
            />
          </Box>
        ))}
      </Document>
    </Box>
  )
}
