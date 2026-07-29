import { Box } from '@mui/material'
import ReactMarkdown, { defaultUrlTransform } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeHighlight from 'rehype-highlight'
import rehypeSlug from 'rehype-slug'
import { DocumentCard } from '../components/doccard/DocumentCard'
import { parseDocRef } from '../components/doccard/docref'
import { ProductionCard } from '../components/productioncard/ProductionCard'
import { parseBatchRef, parseJobRef } from '../components/productioncard/prodref'
import { TaskCard } from '../components/taskcard/TaskCard'
import { parseTaskRef } from '../components/taskcard/taskref'
import { WhiteboardCard } from '../components/whiteboardcard/WhiteboardCard'
import { parseWhiteboardRef } from '../components/whiteboardcard/wbref'
// highlight.js token theme for fenced code blocks. Light-mode palette for now;
// swapping to a dark variant under the app's dark theme is a follow-up polish
// item (the plan defers markdown-rendering refinements to a later iteration).
import 'highlight.js/styles/github.css'

// Markdown renders wiki page bodies: GitHub-flavoured markdown (tables, task
// lists, strikethrough) plus fenced-code syntax highlighting. Prose styling is
// applied via sx selectors on the element tags react-markdown emits, so it reads
// like the rest of the MUI UI without a global stylesheet.
export function Markdown({ children }: { children: string }) {
  return (
    <Box
      sx={{
        color: 'text.primary',
        fontSize: 14,
        lineHeight: 1.6,
        wordBreak: 'break-word',
        '& h1': { fontSize: 26, fontWeight: 700, mt: 3, mb: 1.5, lineHeight: 1.2 },
        '& h2': { fontSize: 21, fontWeight: 700, mt: 3, mb: 1.25, lineHeight: 1.25 },
        '& h3': { fontSize: 17, fontWeight: 600, mt: 2.5, mb: 1 },
        '& h4, & h5, & h6': { fontSize: 15, fontWeight: 600, mt: 2, mb: 0.75 },
        '& h1:first-of-type': { mt: 0 },
        '& p': { my: 1.25 },
        '& a': { color: 'primary.main', textDecoration: 'none', '&:hover': { textDecoration: 'underline' } },
        '& ul, & ol': { pl: 3, my: 1.25 },
        '& li': { my: 0.25 },
        '& blockquote': {
          borderLeft: 3,
          borderColor: 'divider',
          color: 'text.secondary',
          m: 0,
          my: 1.5,
          pl: 2,
        },
        '& code': {
          fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
          fontSize: 12.5,
          bgcolor: 'action.hover',
          px: 0.5,
          py: 0.15,
          borderRadius: 0.75,
        },
        '& pre': {
          bgcolor: 'action.hover',
          p: 1.5,
          borderRadius: 1,
          overflowX: 'auto',
          my: 1.5,
        },
        '& pre code': { bgcolor: 'transparent', p: 0, fontSize: 12.5 },
        '& table': { borderCollapse: 'collapse', my: 1.5, display: 'block', overflowX: 'auto' },
        '& th, & td': { border: 1, borderColor: 'divider', px: 1, py: 0.5, textAlign: 'left' },
        '& th': { bgcolor: 'action.hover', fontWeight: 600 },
        '& hr': { border: 0, borderTop: 1, borderColor: 'divider', my: 2 },
        '& img': { maxWidth: '100%' },
      }}
    >
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeSlug, rehypeHighlight]}
        // Let fls: tokens (doc / task / job / batch / whiteboard) through the
        // URL sanitiser (they never reach the DOM as hrefs — the link component
        // below unfurls them into cards).
        urlTransform={(url) => (url.startsWith('fls:') ? url : defaultUrlTransform(url))}
        components={{
          a: ({ node: _node, href, children: linkChildren, ...rest }) => {
            const docRef = href ? parseDocRef(href) : null
            if (docRef) return <DocumentCard docRef={docRef} />
            const taskRef = href ? parseTaskRef(href) : null
            if (taskRef) return <TaskCard taskRef={taskRef} />
            const batchRef = href ? parseBatchRef(href) : null
            if (batchRef) return <ProductionCard jobRef={batchRef} batchRef={batchRef} />
            const jobRef = href ? parseJobRef(href) : null
            if (jobRef) return <ProductionCard jobRef={jobRef} />
            const boardRef = href ? parseWhiteboardRef(href) : null
            if (boardRef) return <WhiteboardCard whiteboardRef={boardRef} />
            return (
              <a href={href} {...rest}>
                {linkChildren}
              </a>
            )
          },
        }}
      >
        {children}
      </ReactMarkdown>
    </Box>
  )
}
