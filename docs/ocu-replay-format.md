# OCU Replay Script Format — `.ocu-replay`

The `.ocu-replay` format is a simple line-oriented DSL produced by
`pkg/ticket.BuildReplayScript` and consumed by the
`helixqa replay <ticket>` subcommand to deterministically replay a
sequence of automation actions that led to a failure.

## Goals

- Human-readable without tooling
- Lossless round-trip of every `automation.Action` field
- One line per action — easy to diff, annotate, truncate
- No dependencies beyond a basic text parser

## Line format

```
<kind>[:<field>=<value>[:<field>=<value>…]]
```

Each line starts with the action kind (matching the `ActionKind`
string constants in `pkg/nexus/automation/action.go`), followed by
zero or more colon-separated `key=value` pairs. Lines with no extra
fields (e.g. `capture`, `analyze`) carry only the kind token.

Blank lines and lines starting with `#` are treated as comments and
ignored by the parser.

## Action kinds and their fields

| Kind | Fields | Example |
|---|---|---|
| `click` | `at=X,Y` | `click:at=10,20` |
| `type` | `text=<quoted>` | `type:text="hello world"` |
| `scroll` | `at=X,Y`, `dx=N`, `dy=N` | `scroll:at=100,200:dx=0:dy=-10` |
| `key` | `key=<keycode>` | `key:key=enter` |
| `drag` | `from=X,Y`, `to=X,Y` | `drag:from=10,20:to=50,80` |
| `capture` | _(none)_ | `capture` |
| `analyze` | _(none)_ | `analyze` |
| `record_clip` | `around=<ns>`, `window=<ns>` | `record_clip:around=1713400000000000000:window=5000000000` |

### Field details

**`at=X,Y`** — screen coordinate in pixels (integer, comma-separated,
no spaces).

**`from=X,Y` / `to=X,Y`** — drag source and destination coordinates.

**`text=<quoted>`** — Go `%q`-quoted string. The parser must handle Go
string escape sequences (`\"`, `\\`, `\n`, `\t`, etc.). This ensures
special characters in injected text round-trip safely.

**`key=<keycode>`** — portable key identifier from
`pkg/nexus/native/contracts.KeyCode` (e.g. `enter`, `escape`, `tab`,
`arrow_up`). Custom key codes pass through verbatim.

**`around=<ns>` / `window=<ns>`** — Unix nanosecond timestamp and
duration as raw `int64` values. Zero `window` means the replayer
returns all buffered frames.

## Example file

```ocu-replay
# Reproduce HQA-0042: login flow hangs after submit
capture
click:at=640,400
type:text="admin"
key:key=tab
type:text="${HELIX_TEST_PASSWORD}"
key:key=enter
analyze
scroll:at=640,500:dx=0:dy=-200
record_clip:around=1713400000000000000:window=5000000000
```

## Generating a replay script

```go
import "digital.vasic.helixqa/pkg/ticket"

script := ticket.BuildReplayScript(actions)
ev := ticket.Evidence{
    Kind: ticket.EvidenceKindReplayScript,
    Ref:  "replay-HQA-0042.ocu-replay",
}
```

The script string can be written to a `.ocu-replay` file alongside
the ticket markdown and attached as `EvidenceKindReplayScript` evidence.

## Parser requirements

Implementations of `helixqa replay` must:

1. Skip blank lines and comment lines (`# …`).
2. Split each line on `:` (first token = kind, remaining = `key=value` pairs).
3. Split each `key=value` pair on the first `=`.
4. For `text=` values: strip the outer `"` and unescape Go string escapes.
5. For coordinate values (`at`, `from`, `to`): split on `,` and parse as `int`.
6. For `around` and `window`: parse as `int64`.
7. Reject unknown kinds with a descriptive error (no silent skip).

## Version

Format version: **1.0** (2026-04-18). Additions to the field set are
backward-compatible; new kinds require a parser version bump.
