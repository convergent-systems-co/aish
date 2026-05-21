# aish — documentation site

This directory is the source for the [aish manual site](https://convergent-systems-co.github.io/aish/),
built with Jekyll + the `just-the-docs` remote theme and deployed
to GitHub Pages by `.github/workflows/pages.yml` on every push to
`main` that touches `docs/`.

## Layout

```
docs/
├── _config.yml       # Jekyll + just-the-docs config
├── Gemfile           # gems pinned for local preview parity
├── index.md          # landing page
├── files.md          # state on disk
├── environment.md    # env vars aish reads
├── signals.md        # POSIX signal routing
└── man/
    ├── index.md      # manpage index
    └── *.md          # one page per built-in
```

Every manpage follows the classic Unix layout: `NAME`,
`SYNOPSIS`, `DESCRIPTION`, `OPTIONS`, `EXIT STATUS`, `EXAMPLES`,
`FILES`, `SEE ALSO`.

## Local preview

Requires Ruby >= 3.0 and Bundler:

```
cd docs
bundle install
bundle exec jekyll serve
```

The default URL is `http://127.0.0.1:4000/aish/`. The `baseurl`
in `_config.yml` mirrors the production path so internal links
work locally too.

## Adding a manpage

1. Drop a new `man/<name>.md` with front matter:
   ```
   ---
   title: <name>(1)
   parent: Manual pages
   permalink: /man/<name>/
   ---
   ```
2. Link it from `index.md` and any related manpages' `SEE ALSO`.
3. Push. The Pages workflow rebuilds on every `docs/` change.

## Source-of-truth discipline

Manpage content MUST match the actual built-in behavior. The
existing pages were written by grepping the source under
`shell/internal/shell/builtin_*.go` for usage strings,
subcommand switches, and exit-code constants — do the same for
new pages rather than inventing.
