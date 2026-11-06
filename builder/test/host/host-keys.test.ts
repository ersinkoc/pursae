import { describe, expect, it, vi } from 'vitest'
import { isolateHostKeys } from '../../host/HostLayer'

describe('isolateHostKeys', () => {
  it('stops every keydown in the host layer from reaching the builder shortcuts', () => {
    const handlers: Record<string, (e: Event) => void> = {}
    isolateHostKeys({ addEventListener: (type: string, h: (e: Event) => void) => (handlers[type] = h) } as never)
    expect(Object.keys(handlers)).toEqual(['keydown'])
    for (const key of ['Backspace', 'Delete', 'z', 'p', 'ArrowUp', 'd', 'Tab']) {
      const stop = vi.fn()
      handlers.keydown({ key, stopPropagation: stop } as unknown as Event)
      expect(stop).toHaveBeenCalledOnce()
    }
  })
})
