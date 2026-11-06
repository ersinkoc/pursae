# AI stubs (no-AI editor build)

AI is out of scope for now. `builder/vite/noai.ts` resolves every import of an upstream
`src/builder/ai/**` module to the file of the same name here, so the shipped editor carries no AI
code, no provider URLs and makes no AI requests.

Rules for a stub:

- Same file name and the same exported names its upstream importers use; inert behaviour
  (`enabled: false`, components render `null`, providers pass children through).
- Pure non-AI helpers (formatting, e.g. `fmtTokens`) keep working.
- Import upstream code as `@/builder/...`, never with a relative path (a relative path resolves
  inside `builder/host/`).
- No provider host names, `/ai/` paths or keys: `builder/test/audit.mjs` fails the build on them.

When upstream adds an import of a new `ai/` module outside `ai/`, the build stops with a message
naming the missing stub.
