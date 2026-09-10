# Rich View Feature Design

**Date:** 2026-05-22  
**Status:** Approved

## Overview

When a text or code file is browsed with a `?view=rich` URL parameter, the server renders the file as a self-contained HTML page with server-side syntax highlighting via the Chroma library. The default Chroma theme is `monokai`; users can override it with `?theme=<chroma-theme-name>`.

The directory listing is updated so that for text/code files, the **filename text** links to `?view=rich` and the **file icon** links to the raw file. For all other file types, both continue to link to the raw file.

---

## Architecture

### Handler Chain

**Before:**

```text
http.ServeMux → customIndexHandler → http.FileServer(nFS)
```

**After:**

```text
http.ServeMux → richViewHandler → customIndexHandler → http.FileServer(nFS)
```

`richViewHandler` is inserted as the outermost file-serving wrapper. It is wired in `newHandlerFromFS` in `filesystem.go`.

### Request Routing in `richViewHandler`

| Condition | Action |
|---|---|
| `?view=rich` absent | Pass through to next handler |
| `?view=rich` present, path is a directory | Redirect to path without query param |
| `?view=rich` present, file is binary/non-text | Pass through (serve raw) |
| `?view=rich` present, file is text/code | Read, highlight, return HTML page |

---

## Components

### New file: `pkg/gowebserver/richview.go`

```go
type richViewHandler struct {
    baseHandler http.Handler        // next handler in chain
    baseFS      fs.FS               // nestedFS for reading files
    tp          trace.TracerProvider
    tmpl        *template.Template  // rich-view.html
}
```

**Constructor:** `newRichViewHandler(baseHandler, baseFS, tp)` — parses the embedded template, returns `*richViewHandler`.

**ServeHTTP logic:**

1. Check `r.URL.Query().Get("view") == "rich"`. If not, call `baseHandler.ServeHTTP`.
2. Open the file via `baseFS.Open(cleanPath)`. If it's a directory, redirect to the listing.
3. Read up to 10 MB. If file exceeds 10 MB, render an "oversized" HTML page with a raw link.
4. Run `http.DetectContentType(first512bytes)`. If not `text/*`, fall through to raw serving.
5. Determine `theme = r.URL.Query().Get("theme")`. Look up via `styles.Get(theme)`. If nil, use monokai.
6. Detect language: `lexers.Match(filename)` → `lexers.Analyse(content)` → `lexers.Fallback`.
7. Tokenize + format with Chroma HTML formatter (line numbers enabled, CSS classes).
8. Render `rich-view.html` template.

### Template data type

```go
type RichViewReport struct {
    FileName           string
    FilePath           string
    RawURL             string        // link back to raw file
    Language           string        // detected language name
    Theme              string        // active Chroma theme
    ChromaCSS          template.CSS  // inline Chroma style block
    HighlightedHTML    template.HTML // pre-rendered highlighted code
    ApplicationVersion string
}
```

### New embedded template: `pkg/gowebserver/rich-view.html`

A self-contained HTML page. Styling is consistent with `custom-index.html` (dark sidebar header, breadcrumb, content area). Contains:

- Header bar with filename, language badge, theme selector (links to `?view=rich&theme=<name>`), "Raw" link
- Inline `<style>` block from Chroma CSS
- `<pre><code>` block with the Chroma-formatted HTML
- Footer with application version

Themes available via selector: monokai, github, github-dark, dracula, solarized-dark, solarized-light, nord.

---

## Directory Listing Changes

### `DirEntry` struct (in `customindex.go`)

Add field:

```go
IsViewable bool  // true for text/code file types
```

Computed in `customIndexHandler.ServeHTTP` when building the entry list, using `isRichViewable(iconClass string) bool`.

### `isRichViewable` function (in `filetypes.go`)

Returns `true` for these icon classes:
`code`, `terminal`, `text`, `markup`, `stylesheet`, `script`, `config`, `log`, `data`, `doc` (e.g. `.md`), `key`, `certificate`

### `custom-index.html` template changes

For entries where `IsViewable` is true:

- **File icon** (`<a>` around the icon SVG): links to raw file path (unchanged)
- **Filename text** (`<a>` around the name): links to `<filename>?view=rich`

For all other entries: both icon and filename link to raw file (existing behavior).

---

## Error Handling

| Scenario | Behavior |
|---|---|
| File > 10 MB | Render HTML page with "File too large for rich view" message and raw link |
| Binary content type detected | Fall through to raw `baseHandler.ServeHTTP` |
| Unknown/invalid theme | Fall back to monokai silently |
| File open error | HTTP 500 with error text |
| Empty file | Render page with empty code block (Chroma handles gracefully) |

---

## Dependencies

Add to `go.mod`:

```text
github.com/alecthomas/chroma/v2
```

The Chroma library is pure Go, no CGO, widely used in the Go ecosystem (Hugo, etc.).

---

## Testing

### Unit tests: `pkg/gowebserver/richview_test.go`

- `TestRichViewHandler_PassThrough`: requests without `?view=rich` pass through
- `TestRichViewHandler_TextFile`: `.go` file returns HTML with highlighted content
- `TestRichViewHandler_BinaryFile`: binary file falls through to raw serving
- `TestRichViewHandler_Directory`: directory path redirects to listing
- `TestRichViewHandler_OversizedFile`: file over 10 MB returns oversized page
- `TestRichViewHandler_ThemeOverride`: `?theme=dracula` applies dracula theme
- `TestRichViewHandler_InvalidTheme`: unknown theme falls back to monokai

### Unit tests: `pkg/gowebserver/filetypes_test.go`

- `TestIsRichViewable`: validates each icon class returns the correct bool

### Integration tests: `pkg/gowebserver/gowebserver_test.go`

- Extend existing server integration tests: spin up test server, GET `<file>?view=rich`, assert `Content-Type: text/html`, assert response body contains Chroma output markers (`<pre class="chroma">`).

---

## File Inventory

| Action | File |
|---|---|
| Create | `pkg/gowebserver/richview.go` |
| Create | `pkg/gowebserver/richview_test.go` |
| Create | `pkg/gowebserver/rich-view.html` |
| Modify | `pkg/gowebserver/filesystem.go` — wire `richViewHandler` in `newHandlerFromFS` |
| Modify | `pkg/gowebserver/customindex.go` — add `IsViewable` to `DirEntry`, compute it |
| Modify | `pkg/gowebserver/filetypes.go` — add `isRichViewable` |
| Modify | `pkg/gowebserver/custom-index.html` — split icon/name links for viewable files |
| Modify | `go.mod` / `go.sum` — add `github.com/alecthomas/chroma/v2` |
