import fs from 'node:fs'
import path from 'node:path'
import { WORK } from './build-harness.mjs'

/** Folds the per-case results into builder/.work/parity/report.json and prints the diff table. */
export default async function globalTeardown() {
  const dir = path.join(WORK, 'parity', 'results')
  if (!fs.existsSync(dir)) return
  const rows = fs
    .readdirSync(dir)
    .filter((f) => f.endsWith('.json'))
    .map((f) => JSON.parse(fs.readFileSync(path.join(dir, f), 'utf8')))
    .sort((a, b) => a.fixture.localeCompare(b.fixture) || b.width - a.width)
  if (!rows.length) return
  fs.writeFileSync(path.join(WORK, 'parity', 'report.json'), JSON.stringify(rows, null, 2) + '\n')
  const pct = (r: number) => (r * 100).toFixed(3).padStart(8) + '%'
  const lines = rows.map((r) => `${r.fixture.padEnd(20)} ${String(r.width).padStart(5)}  ${pct(r.ratio)}  ${r.ok ? 'ok' : 'OVER'}  live ${r.sizeLive.join('x')} static ${r.sizeStatic.join('x')}`)
  console.log(`\n[parity] differing pixels (target ≤ ${(rows[0].target * 100).toFixed(1)}%):\n${lines.join('\n')}\n[parity] report: ${path.join(WORK, 'parity', 'report.json')}`)
}
