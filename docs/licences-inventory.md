# Licences Inventory — OSS Reference Submodules

Every third-party codebase vendored as a tool submodule (flat `<root>/submodules/<snake_name>/`, legacy `tools/opensource/<name>`; see `docs/flat-tools-manifest.tsv`) must
appear below with its licence declared + compatibility status vs.
HelixQA's own Apache-2.0 licence. Reviewers re-read this table every
time a new submodule lands, and quarterly for existing entries.

**Last audit:** 2026-04-18

---

## Compatibility matrix vs. HelixQA (Apache-2.0)

| Licence | Compatible for *reference* (read source, port patterns) | Compatible for *code import* (compile upstream code into HelixQA binaries) | Notes |
|---|---|---|---|
| MIT | ✅ | ✅ | Preserve original copyright + licence notice on any ported code. |
| Apache-2.0 | ✅ | ✅ | No re-licensing required; carry upstream NOTICE files if present. |
| BSD-2 / BSD-3 | ✅ | ✅ | Preserve copyright + disclaimers. |
| ISC | ✅ | ✅ | Same as BSD. |
| LGPL-2.1 / LGPL-3.0 | ✅ | ⚠️ Dynamic link only | No static linking into HelixQA binaries. |
| GPL-2.0 / GPL-3.0 | ✅ | ❌ | HelixQA's Apache-2.0 choice is incompatible with GPL copyleft. |
| AGPL-3.0 | ✅ | ❌ | Network-use clause forces every downstream user to distribute source if ever served over HTTP. Reference-only gate is loud + logged. |

---

## Per-submodule inventory

| Path | Licence | Status | Action |
|---|---|---|---|
| `tools/opensource/browser-use` | **MIT** | ✅ patterns + code imports both allowed | When porting any file, preserve `Copyright (c) 2024 Gregor Zunic` + the MIT text in a HelixQA header comment. |
| `tools/opensource/skyvern` | **AGPL-3.0** | ⚠️ **Reference only — NEVER import code** | HelixQA re-implements Skyvern's Observe→Plan→Act pattern in native Go from first principles. No file, no function, no line is copied. |
| `tools/opensource/stagehand` | **MIT** | ✅ patterns + code imports allowed | Preserve Browserbase copyright notice on any ported primitive. |
| `tools/opensource/ui-tars` | **Apache-2.0** | ✅ both allowed | Carry upstream NOTICE if present. |
| `tools/opensource/ui-tars-desktop` | **Apache-2.0** | ✅ both allowed | Carry upstream NOTICE if present. |
| `tools/opensource/anthropic-quickstarts` | **MIT** | ✅ both allowed | Preserve Anthropic copyright on any ported helper. |
| `tools/opensource/allure2` | (unchecked) | — | Pre-existing vendored submodule; audit in next quarterly pass. |
| `tools/opensource/appium` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/chroma` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/docker-android` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/docling` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/kiwi-tcms` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/leakcanary` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/llama-index` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/marker` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/mem0` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/midscene` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/moondream` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/perfetto` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/redroid` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/scrcpy` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/shortest` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/signoz` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/testdriverai` | (unchecked) | — | Pre-existing; audit in next pass. |
| `tools/opensource/unstructured` | (unchecked) | — | Pre-existing; audit in next pass. |

---

## Important call-outs

### Skyvern is AGPL-3.0

This is the most important row in the entire matrix. The AGPL-3.0
licence means:

- Any **code imported** from Skyvern into HelixQA would force every
  downstream user of HelixQA (including operators who expose HelixQA
  over HTTP / a network protocol) to make their modifications
  publicly available.
- That is fundamentally incompatible with HelixQA's Apache-2.0
  licence expectations: downstream users of HelixQA can build
  proprietary services on top of it without source-distribution
  obligations.

**Policy:** Skyvern is a pure reference. Every file under
`tools/opensource/skyvern/` is allowed to be opened and read in an
editor to inform HelixQA's own re-implementation. Nothing is ever
copied. A regression grep in the `OC-REF-*` challenge suite will
fail the release if any Skyvern identifier (e.g. `SkyvernAgent`,
`skyvern_action_*`, file paths starting with `skyvern/`) leaks into
HelixQA's compiled packages.

### Porting protocol

When a HelixQA PR ports a pattern from a permissive-licence
upstream:

1. In the ported Go file, add a `SPDX-FileCopyrightText` header
   crediting the original author + a `SPDX-License-Identifier: MIT`
   (or Apache-2.0 / BSD) line alongside HelixQA's own headers.
2. Describe the upstream source in a block comment: which file was
   the inspiration, which SHA was the pin at port time, what
   pattern was taken (not which lines).
3. Add a row in `docs/opensource-references.md::Files HelixQA
   mirrors patterns from` so reviewers can trace back.

### AGPL-safe quarantine

The vendored AGPL directory is quarantined:

```sh
# These paths MUST never appear in any Go build graph.
/tools/opensource/skyvern/
```

The existing go.mod in HelixQA uses `/pkg/...` and `/cmd/...`, so
the quarantine is naturally enforced by Go's module system — Go
never walks `tools/opensource/*` unless a package imports from
there.
