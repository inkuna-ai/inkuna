# Contributing to Inkuna

Thanks for helping build Inkuna. Bug reports, fixes, runtime adapters, docs,
and ideas are all welcome.

## Before you start

- **Bugs and small fixes:** open a pull request directly.
- **New features and anything that changes behaviour:** open an issue first,
  so the approach is agreed before you spend time on it.

Everyone taking part follows the [code of conduct](CODE_OF_CONDUCT.md).
Report a vulnerability privately, as [SECURITY.md](SECURITY.md) describes,
never in a public issue.

## Contributor License Agreement

Before your first pull request can merge, you sign the
[Individual Contributor License Agreement](CLA.md). A bot comments on the
pull request with a link; you sign in with GitHub and agree. You sign once.

**Why we ask.** The agreement confirms that you have the right to contribute
your work, and it gives Madcha LLC a clear license to distribute it. It is a
license, not an assignment, so you keep the copyright. In return, Madcha
commits that every contribution it ships in Inkuna becomes available under
the Apache License 2.0 within two years, like the rest of the code.

**Contributing for your employer?** If your employer owns what you write, it
signs the [Corporate Contributor License Agreement](CLA-CORPORATE.md) first.

## License

Inkuna is fair source, licensed under the
[Functional Source License 1.1](LICENSE.md) (FSL-1.1-ALv2). You may use,
modify, and redistribute it for any purpose except offering a competing
commercial product or service, and each version becomes available under
Apache 2.0 two years after it is made available. It is not an open source
license as the Open Source Initiative defines one.

## Making a change

The conventions every change follows are in
[`.inkuna/skills/conventions/SKILL.md`](.inkuna/skills/conventions/SKILL.md).
In short:

1. Fork the repository and branch from `main`.
2. Keep the pull request to one change, with tests beside the code they
   cover and the doc in `docs/` that describes the behaviour updated with it.
3. Run `task ci` before you push. It is the gate CI runs on macOS, Ubuntu,
   and Windows. A change that touches only `docs/`, the Markdown files in
   the repository root, or `NOTICE` skips that run, and CI checks only the
   docs' frontmatter, links, and diagrams. Inkuna builds and tests with
   Go 1.25 or later; its lint tools need Go 1.26, which `task lint` has
   `go` download for them when yours is older (`GOTOOLCHAIN=auto`).
4. If you add, remove, or upgrade a Go module or an npm package, run
   `task notices`. It rewrites `THIRD_PARTY_NOTICES`, the license text of
   everything the app ships, which `task ci` fails on until it matches.
5. Say what changed and how you verified it in the pull request.

## Releasing

A release is a `vX.Y.Z` tag on a commit on `main`. `inkunactl update` follows the newest one by default, and `inkunactl install` starts a new checkout there ([docs/install.md](docs/install.md#channels)). To make one:

1. In a pull request, set the version in both `wails.json` (`info.productVersion`) and `frontend/package.json` (`version`). `package_test.go` fails when they differ.
2. Once it merges, tag the merged commit on `main` and push the tag:

   ```sh
   git fetch origin && git tag -s v0.2.0 origin/main && git push origin v0.2.0
   ```

3. `.github/workflows/release.yml` checks that the tag is `vX.Y.Z`, matches both versions, and is on `main`, then publishes the release with the titles merged since the previous one. A tag it refuses is not a release: delete it, fix what it named, and tag again.

A pre-release tag such as `v0.3.0-rc.1` is never followed by the stable channel and is not published.
