import fs from 'node:fs'
import path from 'node:path'
import { buildHarness, WORK } from './build-harness.mjs'

/** Builds the live harness and clears the previous run's results. */
export default async function globalSetup() {
  for (const d of ['results', 'images']) fs.rmSync(path.join(WORK, 'parity', d), { recursive: true, force: true })
  fs.mkdirSync(path.join(WORK, 'parity', 'results'), { recursive: true })
  const e = await buildHarness()
  console.log(`[parity] harness: ${e.js}`)
}
