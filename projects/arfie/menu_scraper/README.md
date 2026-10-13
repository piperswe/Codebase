# Restaurant menu scraper

Find a restaurant's menus with Kagi and extract them into a unified JSON document.
All LLM calls use Grafana's Go AI SDK with Anthropic, OpenAI Responses, or OpenRouter.

The reusable clients live in `lib/go/kagi` (`New`, `Client.Search`) and
`lib/go/fetchweb` (`New`, `Fetcher.Fetch`, `Fetcher.Document`). Each accepts a
`Config` for HTTP client injection and per-session limits. Fetch metadata belongs
to `fetchweb`; menu interpretation and the output schema remain in this CLI.

## Run

Set `KAGI_API_KEY` and `OPENAI_API_KEY` in your environment, then run from the
repository root:

```sh
bazel run //projects/arfie/menu_scraper -- --city "Chicago" "Restaurant Name" > menu.json
# Or use the shared Go module:
go run ./projects/arfie/menu_scraper --city "Chicago" "Restaurant Name" > menu.json
```

Provider defaults and credentials (only the selected provider's key is required,
along with `KAGI_API_KEY`):

| Provider | Default model | Environment variable |
| --- | --- | --- |
| `anthropic` | `claude-haiku-4-5-20251001` | `ANTHROPIC_API_KEY` |
| `openai` | `gpt-5.6-luna` | `OPENAI_API_KEY` |
| `openrouter` | `qwen/qwen3.8-27b` | `OPENROUTER_API_KEY` |

For example: `go run ./projects/arfie/menu_scraper -provider anthropic -city Chicago "Restaurant Name"`.
OpenRouter uses its Chat Completions endpoint with structured output enabled;
PDF inputs are sent as base64 file parts for OpenRouter's PDF processing.

Quote the restaurant name and put flags **before** it. The city is optional.
The CLI does not load `.env` files automatically. Both services require API
access and may charge for requests.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--city` | empty | Narrow the restaurant/branch match |
| `--provider` | `openai` | `anthropic`, `openai`, or `openrouter` |
| `--model` | provider-specific | Override with a model supporting vision, tools, and structured output |
| `--timeout` | `5m` | Overall deadline, including final reconciliation |
| `--max-steps` | `20` | Research steps, from 1 to 100 |
| `--schema` | false | Print the complete schema without credentials or API calls |
| `--help` | | Print usage |

Only the final JSON document goes to stdout. Progress and errors go to stderr.
Exit code `0` means `ok` or `partial`, `1` means no menu or an operational error,
and `2` means invalid CLI arguments. Help and schema output also exit `0`.

## Output

[`menu.schema.json`](menu.schema.json) is the versioned JSON Schema contract. It
is embedded into the binary and reused for structured extraction and validation.
Run `menu_scraper --schema` to retrieve it from a built binary.

The top-level object contains:

- `schema_version`: currently `"1"`.
- `status`: `ok`, `partial`, `not_found`, or `error`.
- `restaurant`: selected name, city, address, and website, or `null`.
- `selection_rationale`: why that restaurant/branch was chosen, including uncertainty.
- `extracted_at`: UTC RFC 3339 timestamp supplied by the application.
- `menus`: separate named menus with availability text, notes, sections, and items.
- `sources`: fetched source IDs, URLs, media types, and UTC retrieval timestamps.
- `warnings`: inaccessible content, partial extraction, source caveats, and failures.

Items preserve descriptions, price variants, option groups, dietary labels,
explicit allergens, notes, and source references. A price retains the displayed
`text`, optional `label`, nullable decimal-string `amount`, and nullable ISO
`currency`. Market prices and ranges retain their text with a null amount.
An option price retains the displayed price/supplement; its label/text describes
whether it is an additional charge. Unknown scalar values are null, and absent
collections are empty arrays. No currency conversion or translation is performed.
Absent allergen labels do **not** assert that food is allergen-free.

Item citations contain a `source_id` and nullable, one-based PDF `page`.
Inline data images use their parent page URL with an `#embedded-...` fragment.
`ok` means there are no known extraction gaps, not that every published menu was
found. Check `status` and `warnings` even when the command exits successfully.

## How it works

1. Search Kagi's v1 API using the restaurant name and optional city.
2. Let a bounded Grafana SDK agent inspect pages and select the strongest match.
3. Follow menu links and inspect HTML, images, and PDF links/embeds. Extract each
   selected source using schema-constrained model output.
4. Reconcile the validated fragments, retaining separate menu types and options.
   The application rejects changed/invented items, omitted items, and invalid
   source references. If reconciliation fails, it returns the original validated
   fragments with a `partial` status; duplicate menu sections may remain.

Official restaurant sites and their linked menu services are preferred.
Third-party menus may be used as a fallback, with the agent instructed to report
that choice. Restaurant matching and interpretation still depend on the model;
schema validation verifies structure and provenance references, not factual OCR
accuracy or restaurant ownership.

HTML is parsed without executing JavaScript. Links, tables, JSON-LD, common
lazy-loaded image attributes, `srcset`, inline base64 images, and
`iframe`/`embed`/`object` resources are supported. Downloaded image/PDF bytes are
sent as native model inputs; scanned PDFs do not require local OCR software.
Sites requiring JavaScript, authentication, or CAPTCHA may be unavailable.
CSS background images and arbitrary JavaScript-generated asset URLs are not
discovered.

Each run permits six distinct searches and 30 URL fetch attempts, with successful
reads and failed URLs cached in memory. Up to 60 embedded/fetched documents are
retained while parsing inline images. Documents are limited to 2 MiB for HTML or
plain text, 10 MiB for supported raster images, and 20 MiB for PDFs. Text sent to
the agent is capped at 100,000 bytes and links at 200 per page; truncation of an
extracted source produces a warning. Search results are capped at ten per query.

Website requests have a 30-second timeout and at most five redirects. Only
public HTTP(S) destinations on ports 80/443 are allowed, with DNS destinations
checked and pinned before dialing. API credentials are never sent to websites.
Transient HTTP 429/5xx responses and model calls have at most two retries.
The final minute (or one fifth of a shorter timeout) is reserved for reconciliation.
Ctrl-C/SIGTERM cancels the run. OpenAI Responses requests use `store=false`.

## Verification

```sh
go test ./lib/go/kagi ./lib/go/fetchweb ./projects/arfie/menu_scraper
go test -race ./lib/go/kagi ./lib/go/fetchweb ./projects/arfie/menu_scraper
bazel test //lib/go/kagi:all //lib/go/fetchweb:all //projects/arfie/menu_scraper:menu_scraper_test
bazel run //projects/arfie/menu_scraper -- --schema > /tmp/menu.schema.json
```

Tests are offline. They replay the actual OpenAI Responses protocol through
Grafana's provider, including tool continuation and structured output. Additional
transport tests verify Anthropic and OpenRouter authentication, model selection,
structured output, and image/PDF payloads. Synthetic
HTML, raster-image, text-PDF, and image-only-PDF fixtures check discovery and
native media transport. Mock model responses exercise validation, partial
results, and failure handling; these tests do not measure live OCR accuracy.

For credentialed smoke testing, run the CLI against three restaurants whose
current official menus you have independently checked: one HTML menu, one image
menu, and one linked/embedded PDF (ideally scanned). For each result, compare
restaurant/branch identity, all sections, several prices/options, and source URLs
against the original. Confirm PDF page references and any warnings. Keep live
tests outside the default test suite because they incur API usage and depend on
changing websites and model output.

API references: [Kagi search](https://redocly-api-docs.kagi.com/api/docs/openapi/search/search.md),
[Grafana AI SDK](https://github.com/grafana/ai-sdk), and
[OpenAI file inputs](https://developers.openai.com/api/docs/guides/file-inputs).
