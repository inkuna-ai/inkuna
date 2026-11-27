# ui

`ui/dist` holds the built frontend that `main.go` embeds into the desktop app. It is built from `frontend/` and never committed: `task build`, `task ci`, and `task package` (through `wails build`) build it before Go needs it.

This file keeps the `ui` directory in the repository, so Go code compiles in a checkout where the frontend has not been built. A binary built that way refuses to start and says to run `task build`.
