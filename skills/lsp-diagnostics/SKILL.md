# LSP Diagnostics

Use TypeScript diagnostics as the first code-health check.

- Prefer the `language-server` MCP tools for definitions, references, and diagnostics before broad repo reads.
- Run `npm run lint` after source edits.
- Prefer fixing compiler errors before broader tests.
- Keep diagnostics in the repo trace only after redaction.
- Do not promote Tier 2 source edits with failing diagnostics.
