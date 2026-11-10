import { describe, expect, it } from 'vitest'
import { regenerateAll, type OutdatedItem } from '../../host/regenerate'

const item = (id: number, status = 'publish'): OutdatedItem => ({ id, title: `Page ${id}`, type: 'page', status, renderer: 0 })

describe('regenerateAll', () => {
  it('walks the outdated pages and publishes each again with its status', async () => {
    const calls: string[] = []
    const queue = [item(1), item(2, 'draft'), item(3, 'future')]
    const rest = {
      request: async (method: string, path: string, opts?: { json?: { status: string; baseRev: number } }) => {
        calls.push(`${method} ${path}`)
        if (path.startsWith('web-builder/v2/outdated')) {
          const after = Number(/after=(\d+)/.exec(path)?.[1])
          return { data: { renderer: 1, total: 3, items: queue.filter((i) => i.id > after) } }
        }
        const m = /documents\/(\d+)$/.exec(path)
        if (method === 'GET' && m) {
          const it = queue.find((i) => i.id === Number(m[1]))!
          return { data: { doc: { sections: [] }, page: {}, rev: 7, status: it.status } }
        }
        return { data: { ok: true, _body: opts?.json } }
      },
    }
    const live = { renderStaticAsync: async () => ({ renderer: 1, html: '', css: '', fonts: [], features: [], classes: [] }) }
    const seen: number[] = []
    const res = await regenerateAll({
      rest: rest as never,
      boot: { rendererVersion: 1, rest: { root: '/wp-json/' }, links: { site: 'http://x' } },
      loadRenderer: async () => live as never,
      onProgress: (p) => seen.push(p.done),
    })
    expect(res.finished).toBe(true)
    expect(res.total).toBe(3)
    expect(res.done).toBe(3)
    expect(calls.filter((c) => c.startsWith('POST')).sort()).toEqual(['POST web-builder/v2/documents/1/publish', 'POST web-builder/v2/documents/2/publish'])
    expect(res.failed.map((f) => f.id)).toEqual([3])
    expect(seen[seen.length - 1]).toBe(3)
  })
})
