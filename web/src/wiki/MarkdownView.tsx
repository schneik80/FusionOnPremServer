import { Box, Stack, Typography } from '@mui/material'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Markdown } from './Markdown'

// MarkdownView renders a wiki page in a scroll container with a sticky
// "On this page" table-of-contents pane (adapted from leafwiki's TOC side
// panel). Headings are read from the rendered DOM — rehype-slug has already
// given them stable ids — so the TOC always matches the anchors. Scroll-spy uses
// a scroll listener (not IntersectionObserver): the active heading is the last
// one whose top has crossed a trigger line ~96px below the container top, with
// an at-bottom fallback so short pages still activate their final heading.

interface TocItem {
  id: string
  text: string
  level: number
}

// Only H1–H3 make a useful contents list; deeper headings would just add noise.
const TOC_SELECTOR = 'h1, h2, h3'
// Show the pane once there's enough structure to be worth navigating.
const TOC_MIN_ENTRIES = 3
const TRIGGER_OFFSET = 96

export function MarkdownView({ children }: { children: string }) {
  const { t } = useTranslation('wiki')
  const scrollRef = useRef<HTMLDivElement | null>(null)
  const contentRef = useRef<HTMLDivElement | null>(null)
  const [toc, setToc] = useState<TocItem[]>([])
  const [activeId, setActiveId] = useState<string | null>(null)

  // Rebuild the TOC whenever the rendered content changes.
  useEffect(() => {
    const el = contentRef.current
    if (!el) return
    const heads = Array.from(el.querySelectorAll<HTMLElement>(TOC_SELECTOR))
    setToc(
      heads
        .filter((h) => h.id)
        .map((h) => ({ id: h.id, text: h.textContent ?? '', level: Number(h.tagName[1]) })),
    )
  }, [children])

  // Scroll-spy over the container.
  useEffect(() => {
    const root = scrollRef.current
    const content = contentRef.current
    if (!root || !content || toc.length === 0) {
      setActiveId(null)
      return
    }
    let raf = 0
    const compute = () => {
      raf = 0
      // Near the bottom, the last heading wins even if it never crossed the line.
      if (root.scrollTop + root.clientHeight >= root.scrollHeight - 4) {
        setActiveId(toc[toc.length - 1].id)
        return
      }
      const line = root.getBoundingClientRect().top + TRIGGER_OFFSET
      let active = toc[0].id
      for (const t of toc) {
        const e = content.querySelector<HTMLElement>(`#${CSS.escape(t.id)}`)
        if (!e) continue
        if (e.getBoundingClientRect().top <= line) active = t.id
        else break
      }
      setActiveId(active)
    }
    const onScroll = () => {
      if (!raf) raf = requestAnimationFrame(compute)
    }
    compute()
    root.addEventListener('scroll', onScroll, { passive: true })
    return () => {
      root.removeEventListener('scroll', onScroll)
      if (raf) cancelAnimationFrame(raf)
    }
  }, [toc])

  const scrollTo = (id: string) => {
    contentRef.current
      ?.querySelector<HTMLElement>(`#${CSS.escape(id)}`)
      ?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    setActiveId(id)
  }

  return (
    <Box sx={{ display: 'flex', flex: 1, minHeight: 0 }}>
      <Box ref={scrollRef} sx={{ flex: 1, minWidth: 0, overflowY: 'auto', p: 2 }}>
        <Box ref={contentRef}>
          <Markdown>{children}</Markdown>
        </Box>
      </Box>
      {toc.length >= TOC_MIN_ENTRIES && (
        <Box
          component="nav"
          aria-label={t('toc.ariaLabel')}
          sx={{
            width: 200,
            flexShrink: 0,
            borderLeft: 1,
            borderColor: 'divider',
            overflowY: 'auto',
            p: 1.5,
            display: { xs: 'none', md: 'block' },
          }}
        >
          <Typography variant="overline" color="text.secondary" sx={{ display: 'block', mb: 0.5 }}>
            {t('toc.title')}
          </Typography>
          <Stack spacing={0}>
            {toc.map((t) => (
              <Box
                key={t.id}
                component="button"
                onClick={() => scrollTo(t.id)}
                title={t.text}
                sx={{
                  textAlign: 'left',
                  border: 0,
                  borderLeft: 2,
                  borderColor: activeId === t.id ? 'primary.main' : 'transparent',
                  background: 'none',
                  cursor: 'pointer',
                  font: 'inherit',
                  fontSize: 12.5,
                  lineHeight: 1.4,
                  py: 0.4,
                  pr: 0.5,
                  pl: 1 + (t.level - 1) * 1.25,
                  color: activeId === t.id ? 'primary.main' : 'text.secondary',
                  fontWeight: activeId === t.id ? 600 : 400,
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                  '&:hover': { color: 'text.primary' },
                }}
              >
                {t.text}
              </Box>
            ))}
          </Stack>
        </Box>
      )}
    </Box>
  )
}
