# Contributing to Flock

Thanks for your interest in Flock! Bug reports and feature requests are welcome
as [issues](https://github.com/flock-php/flock/issues/new/choose). For questions and help, use
[Discussions](https://github.com/flock-php/flock/discussions) or
[Discord](https://discord.flockphp.com). Please report security issues privately, as described in
the [security policy](https://github.com/flock-php/.github/blob/main/SECURITY.md).

## Pull requests start with an issue

To make sure work goes into changes that fit the project, pull requests start with an issue in this repository,
except for changes limited to `docs/` and `lang/`:

1. **Open an issue** describing the bug or idea, or comment on an existing one.
2. **Wait for approval.** New issues are marked **`needs triage`** while they are reviewed. An approved issue is either
    - labeled **`help wanted`**, or
    - **assigned to you**.
3. **Open a pull request** against `main` that links the issue with a closing keyword, such as `Fixes #123`, or in the Development sidebar.

An issue assigned to someone else isn't available, even if it still has the `help wanted` label. If an unassigned
`help wanted` issue receives more than one pull request, the first pull request Flock processes claims it. Flock adds
the **`claimed`** label to the issue and pull request, removes `help wanted`, and closes the other pull requests.

Pull requests that need an issue but don't reference an approved one are closed automatically, with a note explaining
why. Once the issue is approved, edit the pull request's description to reference it and it reopens by itself. Adding a
Development sidebar link alone doesn't trigger reopening; edit the description even if the issue is already linked there.

Flock releases an automatic issue claim when its pull request closes without merging, targets a branch other than
`main`, or no longer has the issue as a same-repository closing reference. The release removes only the bot's claim
and assignment. A maintainer assignment or another assignee keeps the issue's current ownership and availability state.

Flock's prebuilt PHP and services are built in [flock-php/binaries](https://github.com/flock-php/binaries), and a
change often needs work in both repositories. When a change needs an update there, open the related issue and pull
request in `flock-php/binaries` too.

## User interface changes

Flock has native apps for macOS, Windows, and Linux. A user interface change must include the matching change on all
three platforms. If the related issue makes a change appropriate on the other platforms, a maintainer may ask you to
complete it before the pull request can merge. A pull request that does not implement the change across all three
platforms may be rejected.

## Documentation

The documentation at [flockphp.com/docs](https://flockphp.com/docs/quick-start) lives in [`docs/`](docs). A pull request
that changes only files in `docs/` and `lang/`, such as a typo fix or a clearer explanation, needs no issue. Use
**Edit this page on GitHub** at the bottom of a page, or open a pull request against `main` directly. If it later changes
files outside `docs/` and `lang/` too, it needs an approved issue like any other.

The website shows the documentation of released versions only, so a change appears there with the
next release. A pull request that adds or changes a feature should update its documentation too.

## Translations

The apps' texts live in [`lang/`](lang), a folder per language. To add or fix translations, edit
`lang/<code>/app.json`, or start a new language as described in [`lang/README.md`](lang/README.md),
run `make i18n`, and open a pull request against `main`. A pull request that changes only files in
`lang/` and `docs/` needs no issue. Changes outside those directories need an approved issue.

A pull request that adds or changes a text in an app needs only the English, in `lang/en/app.json`
under a key; translations are welcome but not required. Changing a text's meaning needs a new
key. [`lang/README.md`](lang/README.md) describes the format and the rules for keys.
