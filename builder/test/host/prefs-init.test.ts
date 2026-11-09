import { expect, it } from 'vitest'
import { HostStorage, HostUi } from '../../host/cms-host'
import type { RestClient } from '../../host/rest'
import { MemoryStorage } from './helpers'

it.each(['boxed', null, 'wide'])('keeps preference %s edited before initialization completes', async (value) => {
  let release!: (value: unknown) => void
  const gate = new Promise((resolve) => { release = resolve })
  const timers = new Map<number, () => void>()
  let seq = 0
  const clock = { set: (f: () => void) => { timers.set(++seq, f); return seq }, clear: (id: unknown) => { timers.delete(id as number) } }
  const puts: unknown[] = []
  const rest = { request: async (method: string, route: string, opts?: { json?: unknown }) => {
    if (method === 'PUT') { puts.push(opts?.json); return { data: {} } }
    return { data: route.endsWith('/prefs') ? await gate : [] }
  } } as unknown as RestClient
  const s = new HostStorage({ rest, userId: 7, postId: 42, ui: new HostUi(clock), local: new MemoryStorage(), timers: clock })
  const loading = s.init()
  s.set('layout-width', value)
  release({ 'pursae-builder:layout-width': 'full', 'pursae-builder:recent-fonts': ['Inter'] })
  await loading
  expect(s.get('layout-width', 'fallback')).toBe(value === null ? 'fallback' : value)
  expect(s.get('recent-fonts', [])).toEqual(['Inter'])
  expect(timers.size).toBe(1)
  await s.flushPrefs()
  expect(puts).toEqual([{ 'pursae-builder:layout-width': value }])
  expect(timers.size).toBe(0)
})
