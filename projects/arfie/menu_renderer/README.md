# Menu renderer

Render a [menu scraper JSON document](../menu_scraper/menu.schema.json) as a
standalone HTML page. The renderer reads stdin and writes only HTML to stdout.
It works offline, with embedded CSS, system fonts, and no JavaScript.

From the repository root:

```sh
bazel run //projects/arfie/menu_renderer < menu.json > menu.html
# Or use the shared Go module:
go run ./projects/arfie/menu_renderer < menu.json > menu.html
```

The responsive, printable page preserves menu ordering, displayed price text,
options and selection limits, explicit dietary labels and allergens, and notes.
Warnings remain visible; extraction metadata and source links are in expandable
details at the bottom. Item citations link to source entries and show PDF page
numbers when supplied. Prices are not recalculated, and currency is not inferred.

Input must be one JSON object with `schema_version` equal to `"1"` and a recognized
status. Malformed JSON, incompatible field types, trailing documents, unsupported
versions, and unknown statuses are rejected. This is not a full JSON Schema
validator: other required-field and semantic constraints are the producer's
responsibility. Unknown fields are ignored. Nullable values and empty collections
are handled gracefully. Only HTTP(S) website/source URLs become external links;
other URLs are displayed as text. Unresolved source IDs remain plain-text citations.

Exit code `0` means successful rendering, including input with `partial`,
`not_found`, or `error` status. Exit code `1` means an input, rendering, or output
failure; `2` means invalid arguments. `--help` exits `0`. Help and diagnostics go
to stderr. Rendering completes in memory before stdout is written, although an
output-device failure can still leave a partial file.

## Development

Edit `menu.templ` and `menu.css`, then use the repository generation workflow:

```sh
mise gencode
go test ./projects/arfie/menu_renderer
bazel test //projects/arfie/menu_renderer:all
```

Keep generated `menu_templ.go` checked in. Generation uses the repository's pinned
templ tool. The renderer owns decoding types matching the schema because the
scraper is a command package; schema changes should update these types and tests.
