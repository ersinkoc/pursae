/**
 * Basic mode's section list thumbnail of a code section (elementsSeam.Thumb, patch 0024): a small
 * scaled, static snapshot of the section itself. The frame is built only once its row is near the
 * viewport (IntersectionObserver), is sandboxed without allow-scripts and shows the neutralised
 * markup with the design's stylesheets (thumb.ts); the builder's kind icon shows until the frame
 * has loaded, and when there is nothing to show or no way to tell the row is visible.
 */
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useB } from '@/builder/store'
import type { Section } from '@/builder/types'
import { kitsCSS } from './kitstyle'
import { SECTION_THUMB_W, THUMB_SANDBOX, sectionThumbDoc } from './thumb'

export function SectionThumb({ s, size, fallback }: { s: Section; size: number; fallback: ReactNode }) {
  const b = useB()
  const box = useRef<HTMLSpanElement>(null)
  const [near, setNear] = useState(false)
  const [loaded, setLoaded] = useState(false)
  useEffect(() => {
    const el = box.current
    if (!el || near || typeof IntersectionObserver === 'undefined') return
    const io = new IntersectionObserver((list) => list.some((x) => x.isIntersecting) && setNear(true), { rootMargin: '120px' })
    io.observe(el)
    return () => io.disconnect()
  }, [near])
  const kitCss = near ? kitsCSS(b.doc) : ''
  const doc = useMemo(
    () => (near ? sectionThumbDoc(s, kitCss, typeof location !== 'undefined' ? location.origin + '/' : '/') : null),
    [near, s.content.html, s.content.head, s.content.body, (s as { local?: unknown }).local, kitCss], // eslint-disable-line react-hooks/exhaustive-deps
  )
  useEffect(() => setLoaded(false), [doc])
  return (
    <span ref={box} data-wb-section-thumb="" className="relative inline-block shrink-0 overflow-hidden rounded-[10px]" style={{ width: size, height: size }}>
      {fallback}
      {doc && (
        <iframe
          title=""
          srcDoc={doc}
          sandbox={THUMB_SANDBOX}
          tabIndex={-1}
          aria-hidden="true"
          onLoad={() => setLoaded(true)}
          className="pointer-events-none absolute left-0 top-0 origin-top-left border-0 bg-white transition-opacity duration-200"
          style={{ width: SECTION_THUMB_W, height: SECTION_THUMB_W, transform: `scale(${size / SECTION_THUMB_W})`, opacity: loaded ? 1 : 0 }}
        />
      )}
    </span>
  )
}
