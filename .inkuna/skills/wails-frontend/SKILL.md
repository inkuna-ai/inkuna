---
name: wails-frontend
description: Changing Inkuna's React view (frontend/) and its Wails bindings, including where Go ends and the view begins, regenerating bindings, Tailwind, tests, and the built ui/dist bundle.
---

# Wails frontend

The desktop UI is React 19, TypeScript, Vite, and Tailwind CSS v4 in `frontend/`. It is a view: Go owns state, validation, and every decision.

## Talking to Go

- The view reaches Go only through the generated Wails bindings in `frontend/wailsjs/`, wrapped by `frontend/src/bridge.ts` (calls) and `frontend/src/events.ts` (events). Components import from those wrappers, never from `wailsjs/` directly.
- `frontend/wailsjs/` is generated. Never edit it by hand. After adding or changing an exported method on `internal/app.Service`, regenerate it with `go tool wails generate module` and commit the result with the Go change.
- Shared shapes live in `frontend/src/types.ts`.
- Go is the only validator. Send the edit to Go and show the reason it returns; do not reimplement a Go rule in TypeScript. The rules editor is the pattern: the form and the YAML tab both round-trip through Go.

## Layout and style

- Views live in `frontend/src/views/`, one file per screen, with a directory (`views/rules/`, `views/agents/`) when a screen has several parts. Shared pieces are in `views/common.tsx`.
- Style with Tailwind utility classes. Add CSS to `frontend/src/index.css` only for what utilities cannot express.
- Format with Prettier (`npm run format --prefix frontend`).

## Checks

- Tests use Vitest and Testing Library, beside the component as `*.test.ts(x)`. Run them with `npm test --prefix frontend -- --run`.
- An `afterEach` that resets or restores mocks, or deletes a binding such as `window.go`, calls `cleanup()` first. Testing Library unmounts only after a describe block's hooks, so an effect still pending would call a reset mock or a binding that is gone, which fails CI intermittently. `src/test/teardown.test.ts` fails when a hook does not.
- `npm run lint --prefix frontend` and `npm run typecheck --prefix frontend` must pass.
- For a visual check without a run, `frontend/mock.html` renders the run detail's Changes tab from fixtures in `frontend/src/mock/`, which stand in for the Go bindings on `window.go`. Start `npm run dev --prefix frontend` and open `/mock.html`. It is never built into `ui/dist`. Extend the fixtures when the Changes tab gains a case, and follow the same pattern for another view.

## The built bundle

`main.go` embeds the built frontend from `ui/dist`, which is ignored and never committed. `task build`, `task ci`, and `task package` build it from `frontend/` first. Without it Go code still compiles (`main.go` embeds `ui`, which always holds `ui/README.md`), but `main_test.go` skips its check of the page's Content-Security-Policy, except under `CI`, and the app refuses to start and says to run `task build`.

An Inkuna worktree has no `frontend/node_modules`, so an Inkuna worker cannot run the frontend build or tests. Verification runs `task deps` and then `task ci`, which builds and tests the frontend; when you change the frontend from an Inkuna step, say under `risks` that the frontend tests have not run until then.
