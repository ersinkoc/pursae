import { expect, it } from 'vitest'
import { Autosaver, HostSession, HostUi } from '../../host/cms-host'
import type { RestClient } from '../../host/rest'
import { makeDoc, PAGE } from './helpers'

it.each(['success', 'failure', 'newer-save'])('cancellation ignores stale %s completion', async (mode) => {
  const timers = new Map<number, () => void>()
  let seq = 0
  const clock = { set: (f: () => void) => { timers.set(++seq, f); return seq }, clear: (id: unknown) => { timers.delete(id as number) } }
  let resolve!: (value: unknown) => void
  let reject!: (error: Error) => void
  const gate = new Promise((ok, fail) => { resolve = ok; reject = fail })
  let calls = 0
  const rest = { request: async () => ({ data: ++calls === 1 ? await gate : { rev: 1 } }) } as unknown as RestClient
  const session = new HostSession(1, false)
  session.lastSavedJson = 'baseline'
  const s = new Autosaver({ rest, postId: 42, session, ui: new HostUi(clock), timers: clock, now: () => 0 })
  const first = s.save(makeDoc('old'), PAGE).catch(() => undefined)
  const running = s.flush()
  expect(calls).toBe(1)
  const canceled = s.cancel()
  const newer = mode === 'newer-save' ? s.save(makeDoc('new'), PAGE) : null
  if (mode === 'success') resolve({ rev: 1 })
  else reject(new Error('transport failed'))
  await Promise.all([first, running, canceled])
  expect(session.lastSavedJson).toBe('baseline')
  if (newer) {
    expect(s.pending).toBe(true)
    await s.flush()
    await newer
    expect(calls).toBe(2)
    expect(session.lastSavedJson).toContain('new')
  } else {
    expect(s.pending).toBe(false)
    expect(timers.size).toBe(0)
  }
})
