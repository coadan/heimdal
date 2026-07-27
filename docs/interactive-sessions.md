# Interactive sessions

A named session keeps one Playwright browser available across commands.
Heimdal records semantic snapshots, actions, assertions, and evidence so the
exploration can later become a deterministic test.

## Observe and act

```bash
heimdal session observe --name qa
heimdal session click e12 --name qa
heimdal session fill e18 "example" --submit --name qa
heimdal session diagnose --name qa --json
```

Use refs from the latest observation and re-observe after navigation or
meaningful DOM mutation. `observe` returns a semantic delta by default; add
`--full` only when the complete tree is needed.

Heimdal keeps common Playwright actions stable across CLI versions, including
targeted `press` and `type`, `fill --submit`, `click --force`, and mouse
movement.

## Wait for visible state

Prefer an explicit wait to snapshot polling or sleeping:

```bash
heimdal session wait --role button --name "Continue" --state enabled --timeout 30s
heimdal session wait --text "Saved"
heimdal session wait --change --settle 300ms
```

For a named browser session, use `--session qa` with `wait`; `--name` denotes
the accessible name paired with `--role`.

## Assert outcomes

```bash
heimdal session expect --role button --name "Continue" --state enabled
heimdal session expect --text "Saved" --state visible
heimdal session expect --url "http://127.0.0.1:3000/done"
heimdal session expect --target e12 --value "ready"
```

Assertions are retained for `session save`.

## Test reconnection

Exercise EventSource or fetch-stream reconnection without restarting the app:

```bash
heimdal session reconnect --request /events --json
heimdal session batch --json -- \
  reconnect --request /events --then \
  wait --text "Updated" --timeout 30s
```

The reconnect command briefly takes the Playwright context offline, restores
it, and can wait for a matching request.

## Batch a known flow

Group stable actions, assertions, and evidence into one agent roundtrip:

```bash
heimdal session batch --session qa --json -- \
  click e8 --then \
  expect --role button --name "Saved" --then \
  evidence save.state "() => ({ saved: true })"
```

Batch execution stops at the first failure. When every step has a stable,
unambiguous locator, Heimdal compiles the flow into one Playwright code
invocation plus one final observation.

## Inspect long explorations

```bash
heimdal session checkpoint "entered checkout" --name qa
heimdal session timeline qa --json
heimdal session timeline qa --failures --limit 20 --json
heimdal session report qa --json
```

Timeline output is bounded to phases, failures, and recent meaningful changes.

## Measure layout

```bash
heimdal session measure --session qa --json
heimdal session measure --session qa --viewport 360x800 --json
heimdal session measure e12 --session qa --json
```

Measurements cover viewport and document geometry, overflow, clipping, touch
targets, controls, and the relevant grid/flex or scroll regions. Use relative
coordinates for spatial controls:

```bash
heimdal session click --within e42 --at 62%,35% --name qa
heimdal session pointer drag --within e42 --from 20%,50% --to 80%,50% --name qa
```

## Save a Playwright draft

```bash
heimdal session save --name qa --test tests/browser/exploration.spec.ts --ready
```

`--ready` reports missing assertions and nonportable actions while still
writing the draft for correction.

## Multiple actors

```bash
heimdal session group start --actors host,guest
heimdal session click --actor guest e12
heimdal session group timeline --json
heimdal session group stop
```

Actors share one app fixture but keep independent browser state.
