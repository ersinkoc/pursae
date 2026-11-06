/**
 * Stand-in for upstream src/builder/ai/providers.ts (no-AI build): no provider list, no model or
 * key handling. The pure formatting helpers are kept, copied from upstream.
 */
export const fmtTokens = (n: number) => (n >= 1_000_000 ? `${(n / 1_000_000).toFixed(2)}M` : n >= 1000 ? `${(n / 1000).toFixed(1)}k` : `${n}`)
export const fmtCost = (n: number) => (n === 0 ? '$0' : n < 0.01 ? `$${n.toFixed(4)}` : `$${n.toFixed(2)}`)

export const PROVIDERS: never[] = []
