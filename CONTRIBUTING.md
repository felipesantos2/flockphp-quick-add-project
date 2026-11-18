# Contributing to Flock

Thanks for your interest in Flock! Bug reports and feature requests are welcome as
[issues](https://github.com/flock-php/flock/issues/new/choose). For questions and help, use
[Discussions](https://github.com/flock-php/flock/discussions) or
[Discord](https://discord.flockphp.com). Please report security issues privately, as described in
the [security policy](https://github.com/flock-php/.github/blob/main/SECURITY.md).

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

## Documentation

The documentation at [flockphp.com/docs](https://flockphp.com/docs/quick-start) lives in
[`docs/`](docs). A pull request that changes only files in `docs/`, such as a typo fix or a
clearer explanation, needs no issue: use **Edit this page on GitHub** at the bottom of a page, or
open a pull request against `main` directly. If it later changes files outside `docs/` too, it
needs an approved issue like any other.

The website shows the documentation of released versions only, so a change appears there with the
next release. A pull request that adds or changes a feature should update its documentation too.

## Translations

The apps' texts live in [`lang/`](lang), a folder per language. A pull request that only adds or
fixes translations needs no issue, like the docs: edit `lang/<code>/app.json`, or copy
`lang/en/app.json` to start a new language, run `make i18n` and open a pull request.

A pull request that adds or changes a text in an app needs only the English, in `lang/en/app.json`
under a key; translations are welcome but not required. Changing a text's meaning needs a new
key. [`lang/README.md`](lang/README.md) describes the format and the rules for keys.
