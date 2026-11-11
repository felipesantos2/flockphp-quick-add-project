# Contributing to Flock

Thanks for your interest in Flock! Bug reports, ideas and questions are welcome as
[issues](https://github.com/flock-php/flock/issues).

## Pull requests start with an issue

To make sure work goes into changes that fit the project, every pull request starts with an issue:

1. **Open an issue** describing the bug or idea, or comment on an existing one.
2. **A maintainer approves it** by either
   - labelling it **`help wanted`**: anyone may send a pull request for it, or
   - **assigning it to you**: it's yours to work on.
3. **Open a pull request** against `main` that references the issue, e.g. `Fixes #123`.

Pull requests that don't reference an approved issue are closed automatically, with a note
explaining why. Once the issue is approved, edit the pull request's description to reference it
and it reopens by itself.

Flock's prebuilt PHP and services are built in [flock-php/binaries](https://github.com/flock-php/binaries),
and a change often needs both. An approved issue in either repository counts for a pull request in
the other: reference it as `flock-php/binaries#123` or with a link to it.

## Development guide

Flock has a shared Go CLI and daemon, plus a native app for each platform.

### Where to start

| What you are changing | Start here |
| --- | --- |
| CLI entry point | [`cmd/flock/main.go`](cmd/flock/main.go) |
| Commands and registration | [`internal/cli/`](internal/cli) and `NewRootCommand` in [`root.go`](internal/cli/root.go) |
| Daemon startup and applying configuration | [`internal/cli/daemon.go`](internal/cli/daemon.go) and [`internal/daemon/daemon.go`](internal/daemon/daemon.go) |
| HTTP API routes and Go client | `NewHandler` in [`internal/api/api.go`](internal/api/api.go), plus the feature's files in [`internal/api/`](internal/api) |
| Configuration and project detection | [`internal/config/`](internal/config) and [`internal/project/`](internal/project) |
| Serving local sites | [`internal/web/web.go`](internal/web/web.go), which configures embedded Caddy |
| Linux app | [`linux/src/main.rs`](linux/src/main.rs); API client: [`api.rs`](linux/src/api.rs) |
| macOS app | [`macos/Sources/FlockBar/Main.swift`](macos/Sources/FlockBar/Main.swift); API client: [`Client.swift`](macos/Sources/FlockKit/Client.swift) |
| Windows app | [`windows/Flock.Tray/Program.cs`](windows/Flock.Tray/Program.cs); API client: [`FlockClient.cs`](windows/Flock.Kit/FlockClient.cs) |

The CLI parses a command, calls its helpers and, when needed, talks to the daemon.
Native apps call the daemon's API. The daemon manages services and applies the web
server configuration; it is not the PHP application being served.

For a hosted project's front controller, look at `RootFor` in
[`internal/project/root.go`](internal/project/root.go). The document root comes
from the site's local override, then `root=` in its `.flockrc`, then the first of
`public/`, `www/`, `web/` and `webroot/` containing `index.php`, otherwise the project
folder itself. This is why Laravel normally uses `public/index.php`, while plain
PHP and the WordPress template use `index.php` in the project folder.

### Adding a feature

1. **Find a similar action first.** Follow one command or API route from input to
   the code that does the work. For a CLI example, see
   [`internal/cli/app.go`](internal/cli/app.go).
2. **Implement only the layers the feature needs.** Keep CLI arguments and output
   in `internal/cli`. Reuse existing helpers; if behavior must be shared with the
   API or native apps, put it in the appropriate Go package under `internal/`.
3. **Wire up the entry point.** New CLI commands use Cobra constructors named
   `new<Name>Command`, validate arguments with `Args`, return errors from `RunE`, and
   are registered in `NewRootCommand` or a parent command's `AddCommand`.
4. **Use the existing state flow.** CLI configuration changes use `load()`, the
   config helpers and `s.commit()` to save and request a daemon reload. API
   configuration changes use `Backend.Mutate`; do not write configuration directly
   from a native app.
5. **Add API/UI support only when required.** Follow the existing route registration
   in `NewHandler`, add a typed Go client method, and update the affected native
   clients and screens. A CLI-only command does not automatically become an API
   endpoint or an app button.
6. **Test and document the behavior.** Add focused tests next to the changed code,
   including failures and existing behavior that must stay unchanged. Update the
   relevant pages in [`docs/`](docs), and keep commits small and focused.

For example, adding another `flock app create --type <type>` means updating type
validation and dispatch in [`app.go`](internal/cli/app.go), implementing its setup
helper, extending the tests and updating [`docs/cli.md`](docs/cli.md). It does not
require changing the CLI entry point or Caddy routing.

### Build and validate

Use the Go version declared in [`go.mod`](go.mod). From the repository root:

```sh
make build                                      # builds bin/flock
go run ./cmd/flock app create --help              # checks command registration
go test ./internal/cli -run TestAppCreate -count=1 # example: tests for one feature
go test -race ./internal/cli                      # CLI regression tests
go run ./tools/docslint                          # checks the published docs
```

Choose the test package and selector for your change. Before a broader submission,
[`make test` and `make lint`](Makefile) run the full Go tests and static/format checks.
Native apps also need their platform's checks; see
[Building from source](README.md#building-from-source) and the
[Windows development guide](windows/README.md).

Use temporary directories and `FLOCK_HOME` in tests, as the CLI tests do, instead
of modifying your installed Flock environment. `FLOCK_HOME` changes the data
directory, not the machine's ports or DNS.

## Documentation

The documentation at [flockphp.com/docs](https://flockphp.com/docs/quick-start) lives in
[`docs/`](docs). A pull request that changes only files in `docs/`, such as a typo fix or a
clearer explanation, needs no issue: use **Edit this page on GitHub** at the bottom of a page, or
open a pull request against `main` directly. If it later changes files outside `docs/` too, it
needs an approved issue like any other.

The website shows the documentation of released versions only, so a change appears there with the
next release. A pull request that adds or changes a feature should update its documentation too.
