#!/usr/bin/env node
/**
 * Writes web/web-builder/THIRD_PARTY_LICENSES.txt: the license notices of the npm packages bundled into
 * the built Pursae Web Builder (editor, site runtime and CSS, server render bundle). Run by
 * scripts/build.mjs after the vite builds; on its own: node scripts/licenses.mjs (needs node_modules).
 *
 * Bundled = the production dependency closure of builder/package.json (resolved through
 * package-lock.json), plus tailwindcss, whose generated CSS (preflight and utilities) ships in the
 * editor and site stylesheets. Build-only tools (vite, typescript, vitest, …) are not bundled.
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const BUILDER = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
export const LICENSES_FILE = 'THIRD_PARTY_LICENSES.txt'

/** Dev dependencies whose output is copied into the dist (generated CSS). */
const SHIPPED_DEV = ['tailwindcss']

/** The bundled packages, sorted by name: [{ name, version, license }], from package-lock.json only. */
export function bundledPackages(builder = BUILDER) {
  const pkg = JSON.parse(fs.readFileSync(path.join(builder, 'package.json'), 'utf8'))
  const lock = JSON.parse(fs.readFileSync(path.join(builder, 'package-lock.json'), 'utf8')).packages
  const seen = new Map()
  const visit = (name) => {
    if (seen.has(name)) return
    const entry = lock[`node_modules/${name}`]
    if (!entry) throw new Error(`package-lock.json has no node_modules/${name}`)
    seen.set(name, { name, version: entry.version, license: entry.license ?? 'UNKNOWN' })
    for (const dep of Object.keys(entry.dependencies ?? {})) visit(dep)
  }
  for (const name of [...Object.keys(pkg.dependencies ?? {}), ...SHIPPED_DEV]) visit(name)
  return [...seen.values()].sort((a, b) => a.name.localeCompare(b.name))
}

function licenseText(dir) {
  const files = fs.readdirSync(dir).filter((f) => /^(licen[cs]e|copying|notice)(\.(md|txt))?$/i.test(f)).sort()
  if (files.length === 0) return null
  return files.map((f) => fs.readFileSync(path.join(dir, f), 'utf8').trim()).join('\n\n')
}

function repoURL(meta) {
  const r = typeof meta.repository === 'string' ? meta.repository : meta.repository?.url
  if (!r) return meta.homepage ?? ''
  return r.replace(/^git\+/, '').replace(/\.git$/, '').replace(/^git:\/\//, 'https://').replace(/^github:/, 'https://github.com/')
}

/** Renders the notices file; throws when a bundled package has no license file in node_modules. */
export function renderLicenses(builder = BUILDER) {
  const rule = '='.repeat(78)
  const out = [
    'Pursae Web Builder: third-party software notices',
    rule,
    '',
    'The files in this directory are built by builder/scripts/build.mjs from the Pursae Web Builder',
    'sources (builder/host, and builder/upstream = https://github.com/pursae/Builder at the commit in',
    'BUILD.json, patched with builder/patches/*.patch) and bundle the npm packages listed below. Each',
    'package keeps its own license; its notice is reproduced in full. See NOTICE at the repository',
    'root for the licensing of Pursae itself.',
    '',
  ]
  const missing = []
  for (const p of bundledPackages(builder)) {
    const dir = path.join(builder, 'node_modules', ...p.name.split('/'))
    const meta = JSON.parse(fs.readFileSync(path.join(dir, 'package.json'), 'utf8'))
    if (meta.version !== p.version) throw new Error(`node_modules/${p.name} is ${meta.version}, package-lock.json says ${p.version}; run npm ci`)
    const text = licenseText(dir)
    if (!text) missing.push(p.name)
    out.push(rule, `${p.name} ${p.version} (${p.license})`, repoURL(meta), rule, '', text ?? '', '')
  }
  if (missing.length) throw new Error(`no license file in node_modules for: ${missing.join(', ')}`)
  return out.join('\n').replace(/\r\n/g, '\n').trimEnd() + '\n'
}

export function writeLicenses({ builder = BUILDER, outDir }) {
  const file = path.join(outDir, LICENSES_FILE)
  fs.mkdirSync(outDir, { recursive: true })
  fs.writeFileSync(file, renderLicenses(builder))
  return file
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const file = writeLicenses({ outDir: path.resolve(BUILDER, '..', 'web', 'web-builder') })
  console.log(`[web-builder] wrote ${path.relative(path.resolve(BUILDER, '..'), file)}`)
}
