# taskman web UI e2e suite

Playwright tests that drive the kanban web UI (`taskman serve`) end to end:
JSON API contract, board rendering and filters, drag-and-drop status and
priority mutations, the dialog lifecycle actions, and screenshot
paste/drop/upload/serving.

## Prerequisites

- A running server, hosted on port 8311 by default:
  `taskman serve -addr 127.0.0.1:8311`
- A sandbox project in the store. Every mutation the suite makes writes
  through to the store and auto-commits there, so the suite refuses to run
  against a project it isn't told to use:

  ```sh
  mkdir -p ~/.taskman/e2e-sandbox/tasks
  ```

- Node and the Playwright chromium browser:

  ```sh
  cd e2e
  npm install
  npm run install-browsers
  ```

## Running

```sh
cd e2e
npm test
```

| Variable       | Default                 | Meaning                                  |
| -------------- | ----------------------- | ---------------------------------------- |
| `E2E_BASE_URL` | `http://localhost:8311` | Server under test                        |
| `E2E_PROJECT`  | `e2e-sandbox`           | Store project the suite reads and writes |

Global setup seeds six fixture tasks (titles prefixed `seed: `) into the
sandbox and reconciles their statuses on every run, so read-only specs have
deterministic content. Mutation specs create uniquely-named tasks and drive
them to done, which accumulate in the sandbox's done column; reset the
sandbox any time by deleting and recreating its directory in the store.

Tests run single-worker on purpose: mutations share one on-disk store and
one priority order file, so parallel workers would race.
