# Search and download

[README](../README.md) · [Configuration](configuration.md) · [Troubleshooting](troubleshooting.md)

Complete the [quick start](../README.md#quick-start) for online commands.
The local bookshelf and file previews work offline without signing in.

Run `zlib tui` in a terminal for the persistent shell: type a query or use `/` to
choose a command. See [terminal controls](tui.md). The commands below also work
directly, including in scripts.

## Local bookshelf

`zlib library` opens your bookshelf. Use `add` for a single file or `scan`
to index a directory recursively. Files stay in their original locations.

```sh
zlib library scan ./books
zlib library add ./reading/sample.epub
zlib library list
zlib library list "sample" --json
zlib library status 1 reading
zlib library tags 1 reference "book club"
zlib preview ./books/sample.pdf --page 2
zlib preview ./reading/sample.epub
zlib library remove 1
```

Use the local ID from `list` for edits. Reading status can be `unread`,
`reading` or `read`. The tags command replaces the entry's tags; omit tag
arguments to clear them. Recently opened books appear first. The index lives
in `~/.config/zlib/library.db`.
Each format and edition can have its own entry. Removing an entry keeps
the file and excludes its path from later scans until explicitly added again.

Downloads into a scanned directory are indexed after saving. Run `scan`
again to discover files added by other programs, refresh metadata and mark
missing files. A directory-confined MCP server only writes inside its allowed
directory; scan its download folder from the CLI to update your bookshelf.

`preview` shows a local PDF page or an EPUB chapter. In the terminal use
the arrow keys to navigate, or pass `--page N` to print the text of page or
chapter N. Piped output defaults to the first page or chapter. EPUB text
works with the standalone binary. For PDF previews, install [Poppler](https://poppler.freedesktop.org/)
and make `pdfinfo` and `pdftotext` available on PATH. Add `pdftoppm` for
page images in terminals with Kitty graphics or Sixel. Roll the wheel or use
`+` and `-` to zoom a PDF image. Press `0` to fit it again. For scanned pages
without selectable text, use image mode or press `o` to open a desktop reader.

## Search

Open the terminal picker without a query:

```sh
zlib search
```

Enter a query, select with `↑/↓`, change pages with `←/→`, and press `Enter` to
download. For a plain table, supply the query on the command line:

```sh
zlib search "dune"
zlib search "dune" --page 2
zlib search "python crash course" --ext epub --ext pdf
zlib search "civilized to death" --full-title
```

`--ext` is repeatable; `--format` is its alias. `--full-title` prevents title
truncation in the table. Use `--json --count 10` for [scripts and agents](agents.md).

## Download a result

Copy the complete reference from a result's ID column or JSON `id` field.
EAPI references contain both the numeric ID and hash, separated by `:`.
Replace `id:hash` in these examples with that reference:

```sh
zlib download "id:hash"
zlib download "id:hash" --dir ./books
zlib download "id:hash" --dir "~/Downloads"
```

Create the destination directory first. `--dir` expands a leading `~`; without
the flag, zlib uses `ZLIB_DOWNLOAD_DIR`, then the current directory. See
[download settings](configuration.md#download-settings) to save a default.

Press `Ctrl+C` to cancel a download; the CLI removes the incomplete file.
After [Kindle setup](kindle.md), add `--send-to-kindle` to send the downloaded file.
After [WeRead login](weread.md), add `--send-to-weread` to import it into WeRead.

Without a terminal, `download` prints `Downloading:` and a progress line per 10%
to stderr, then `Saved to:` with the absolute path on stdout. `--json` replaces
that last line with one JSON document and skips the folder prompt:

```json
{"id":"id:hash","name":"Dune","path":"/home/me/books/Dune.epub","size":1048576}
```

With `--send-to-kindle`, the document also has `kindle`: `{"sent":true,"to":"..."}`,
or `{"sent":false,"error":"..."}` when the book was saved but delivery failed.
`--send-to-weread` adds `weread` the same way: `{"imported":true,"book_id":"..."}`
or `{"imported":false,"error":"..."}`.
`history --download` accepts `--json` the same way.

When Z-Library's record has a valid ISBN, the download writes it into the saved
file's metadata before any Kindle or WeRead delivery, so a later
[`zlib meta --fetch`](metadata.md#look-a-book-up) finds the exact edition. The
JSON document then has `isbn`: `{"scheme":"isbn","value":"978..."}`, or
`{"scheme":"zlib-isbn","value":"978..."}` when the record's title did not agree
with the file's, or the record lists the ISBNs of several editions. An existing
ISBN is never replaced. An EPUB also records the Z-Library book ID (`id:hash`)
as the identifier `zlib-id`, so the file can always be traced back to its
record; the JSON document then has `zlib_id`, and `zlib meta` shows it as
`Z-Library ID`. Other formats have no place for an identifier of their own and
keep only the ISBN. Add `--no-embed` to leave
the file untouched. Downloads from a direct URL (`history --download`) have no
record and write nothing.

## Saved books, recommendations and history

In the history browser, press `d` to delete the selected entry, then `y` to
confirm or `n` / `Esc` to cancel. This removes the remote history entry only;
local book files remain. Scripts can delete directly with
`zlib history --delete "id:hash"` (also accepts a numeric ID); add `--json`
for a machine-readable success response.

```sh
zlib favorites
zlib recommended
zlib most-popular
zlib history
```

`favorites` shows your account collection. In the terminal picker,
select with `↑/↓` and press `Enter` to download. Saved books and history support
`←/→` pagination; recommendations and popular books are single lists.

For non-interactive output, use an explicit page or JSON:

```sh
zlib favorites --page 2
zlib favorites --json
zlib recommended --json
zlib most-popular --json
zlib history --page 1
zlib history --format epub
zlib history --json
```

To download a history entry directly, copy its full book reference:

```sh
zlib history --download "id:hash" --dir ./books
```

## Account and appearance

```sh
zlib profile
zlib profile --json
zlib logout
```

`profile` shows account details and daily download quota. JSON output excludes
authentication keys but includes personal account fields; keep it private.
`logout` removes the saved session.

```sh
zlib theme
zlib theme catppuccin-mocha
```

`zlib theme` opens a searchable list with a zlib search and download preview. Use ↑/↓ to
choose, type to filter, Enter to save, or Esc to clear the filter and close.
With redirected output, it prints the current and available themes instead.
`ZLIB_THEME` overrides the saved theme. See [Themes](themes.md) for the built-in
names and preview controls.

## Book metadata

```sh
zlib meta book.epub
zlib meta book.epub -t "New Title" -a "Jane Doe & John Roe" --cover cover.jpg
```

`meta` shows or edits the title, authors, series, identifiers, cover and other
metadata inside a local file, offline; `--fetch` looks the book up online. See
[E-book metadata](metadata.md).

For interactive controls, see [Terminal interface](tui.md). For exact flags and
defaults, run `zlib <command> --help`.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success, or you left a list without choosing a book |
| 1 | Other error |
| 2 | Usage error: unknown command or flag, wrong number of arguments |
| 3 | Not logged in, or the saved session expired; run `zlib login` (or `zlib weread login` for WeRead) |
| 4 | Network: a host (a mirror, or GitHub for `update`) is unreachable, the mirror is behind DiamWall, `doctor` found no usable domain, `mirrors add` found no Z-Library at the domain, or every source of `meta --fetch` failed |
| 5 | The book was saved, but Send to Kindle or WeRead failed |
| 130 | You cancelled the download or its folder prompt, or declined to trust a mirror |

Errors go to stderr. A network error names the host; for a Z-Library mirror it
suggests `zlib doctor`.
