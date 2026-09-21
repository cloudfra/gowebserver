# Grid, Slideshow and Thumbnails Design

Status: approved in conversation, pending written-spec review.

## Goals

Make the photo/video grid and slideshow in `pkg/gowebserver/custom-index.html`
fast and touch friendly on phones and iPads, with a modern look. Scrolling a
large directory must not lag, and a card under the mouse must get its preview
first.

## Problems in the current code

- The grid `<img>` elements load full-size originals. The server has no
  thumbnail or resize support, so large directories lag and fast scrolling
  janks.
- Video previews mount through a FIFO queue (4 every 200ms). Hovering a card
  does not move it forward. Hover play uses inline `onmouseenter` attributes
  and does not work on touch.
- The photo name tag (`.photo-meta`), the only way into the slideshow, is
  revealed only on hover/focus, so it is unreachable on touch.
- Slideshow: `ssInitOpacities`, `ssUpdateOpacities`, `ssShowNeighbors` and
  `ssMountRange` loop over every item on each navigation and drag start
  (O(N) style writes per swipe). Separate touch and mouse code paths, and a
  `touchstart` `preventDefault()` that swallows taps. No double-tap zoom,
  swipe-down dismiss, tap-to-toggle controls, counter or caption. Uses
  `100vw`/`innerWidth` with no `dvh` or safe-area handling. 82vw slides with
  peeking neighbors are poor on phones.

## Decisions

- Slideshow entry stays as the click on the photo name tag. Clicking the photo
  itself opens the raw image (unchanged).
- On touch devices (`hover: none`) the name tag is always visible as a slim
  strip so the slideshow is reachable.
- Thumbnails are generated server side, on demand, cached in a private
  `ufs` `memory://` filesystem with a byte-budget LRU on top.
- Video previews stay client side (`<video preload=metadata>`). No ffmpeg.
- Not in scope: justified-row layout, date scrubber, on-disk thumbnail cache,
  video thumbnails.
- Implementation order: server first (Phase 1), then client (Phase 2).

## Phase 1: Server thumbnails

### Behavior

`GET <image path>?thumb=<N>` where `N` is one of `256`, `512`, `1024`.

- Any other value of `thumb` returns `400`.
- Non-image paths ignore `thumb` and are served normally.
- Cache hit: serve the cached bytes with `http.ServeContent` (gives `Range`,
  `If-Modified-Since`, `ETag` handling). The modtime passed to `ServeContent`
  is the **source** file's modtime, not the cache entry's: `memfs` stamps
  entries with their creation time, which would change after every eviction
  and regeneration.
- Cache miss: open the source through the existing `fs.FS`, decode, apply EXIF
  orientation, resize so the longest edge is `N` (never upscale), encode as
  JPEG (quality ~80), or PNG when the source has an alpha channel. Store, then
  serve.
- Response headers: `Content-Type`, `Cache-Control: public, max-age=86400`,
  and a strong `ETag` derived from the cache key.
- Fallback: when the format cannot be decoded (HEIC, AVIF, SVG, corrupt file)
  or the image exceeds the pixel limit, serve the original file unchanged. The
  response is never an error for a servable source.

### Components

- `pkg/gowebserver/thumbnail.go`: `thumbnailer` type.
  - `newThumbnailer(ctx, budgetBytes) (*thumbnailer, error)`
  - `(*thumbnailer).serve(w, r, srcFS fs.FS, name string, size int) bool`
    returns false when the caller should fall through to the normal file
    handler.
  - `(*thumbnailer).Close()`
- The `thumbnailer` owns a private `ufs.New(ctx, "memory://")` instance that is
  separate from the served filesystem, so thumbnails never appear in
  directory listings.
- `customIndexHandler.ServeHTTP` delegates to the thumbnailer when `thumb` is
  present. One construction site in `newCustomIndex`.

### Cache

- Key: `t<size>/<hash(path)>-<source mtime unix>-<source size>.<ext>`. Flat
  layout. A changed source produces a new key, so nothing is stale and old
  entries age out.
- Budget: default 256 MiB. A config setting (flag and YAML, in `config.go`)
  overrides it. `0` disables thumbnails, in which case `?thumb=` falls through
  to the original.
- Eviction: the thumbnailer tracks entry size and recency. When the total
  exceeds the budget it evicts least recently used entries with `Remove` until
  under budget. `ufs` memfs has no built-in cap or eviction.
- Concurrent requests for the same key are collapsed to a single decode.

### Guardrails

- At most `runtime.NumCPU()` decodes run at once (semaphore).
- `image.DecodeConfig` runs first. Images over 100 megapixels fall back to the
  original.
- EXIF orientation is read for JPEG (orientation tag) and applied after
  decode. Go's stdlib does not do this and iPhone photos would otherwise be
  sideways.
- New dependency: `golang.org/x/image` (scaling, WebP decode). Nothing else.

### Observability

Prometheus counters for thumbnail cache hit, miss, fallback and eviction using
the existing monitoring setup in `monitoring.go`, plus a gauge for cache bytes.

### Tests (`thumbnail_test.go`, `customindex_test.go`)

- Size ladder: valid sizes resize with longest edge `N`, no upscale, invalid
  size returns 400.
- EXIF rotation (all 8 orientations for a small JPEG fixture).
- Alpha source produces PNG, opaque source produces JPEG.
- Corrupt file, HEIC/SVG extension and over-limit image fall back to the
  original bytes.
- Cache hit avoids a second decode; changed mtime creates a new key.
- LRU eviction under a tiny budget removes the oldest entry and stays under
  budget.
- Conditional request returns `304`.
- Concurrent identical requests decode once (`-race`).
- Budget `0` disables thumbnails.
- Works over a zip archive source and a local directory source.

## Phase 2: Client

Delivered after Phase 1 is merged and verified. Two steps: grid, then
slideshow. All changes are in `custom-index.html` plus render tests in
`customindex_test.go`.

### 2a. Grid

- Photo cards: `<img src="…?thumb=256" srcset="…?thumb=256 1x, …?thumb=512 2x"
  loading="lazy" decoding="async">` with `onerror` fallback to the original
  `src`.
- `content-visibility: auto` with a fixed `contain-intrinsic-size` on cards.
- Hover priority (mouse pointers only, `pointerenter`):
  - Photos: swap to the 512 thumbnail with `fetchpriority="high"`.
  - Videos: move the card to the front of the mount queue and start play as
    soon as metadata is ready. Inline `onmouseenter`/`onmouseleave` attributes
    are replaced by listeners.
- Fast scroll: detect scroll velocity, pause video mounting while it is high,
  and mount only cards that settle into view. Off-screen videos are unmounted
  as today.
- Touch (`hover: none`): `.photo-meta` is always visible as a slim strip.
  Tapping the strip opens the slideshow, tapping the photo opens the raw file.

### 2b. Slideshow

- Performance: per-item work limited to the current slide and neighbors;
  placeholder divs remain; drag/pan handlers run in `requestAnimationFrame`
  and write only `transform`; `will-change` only while dragging; cached rects;
  `prefers-reduced-motion` respected.
- Progressive loading: slide shows the 1024 thumbnail first, then swaps to the
  original after `img.decode()` for the current slide only. Neighbors (±2)
  preload the 1024 thumbnail only. Zooming past about 1.5x ensures the
  original is loaded. Unmounting keeps the memory ceiling from #367.
- Input: one Pointer Events path replacing touch and mouse handlers, with
  `setPointerCapture`. Axis lock after about 8px (horizontal slide vs vertical
  dismiss).
- Gestures: double-tap/double-click zoom (1x to 2.5x at the tap point), pinch
  around the midpoint, pan clamped to image bounds, swipe down to dismiss
  (slide follows finger, backdrop fades, close or spring back), single tap
  toggles controls, mouse idle auto-hide retained.
- History: opening pushes a history entry, back/swipe-back closes the
  slideshow.
- Layout: phones and coarse pointers get full-bleed slides. The 82vw carousel
  remains for desktop and wide iPad layouts. `100dvh` and
  `env(safe-area-inset-*)`. 44px minimum touch targets. Prev/next hidden on
  touch, kept for mouse. Counter ("3 / 42"), filename caption and an
  "Open original" link.
- Fullscreen: opening the slideshow requests browser fullscreen on the overlay
  (with the `webkit` prefix for iPad Safari; a no-op where unsupported, such as
  iPhone Safari). Closing exits it, and leaving fullscreen by any other route
  (Esc, browser UI) closes the slideshow too.
- Kept: auto-play, keyboard navigation, wheel zoom, native video controls.

### Client testing

No browser test harness exists in the repo. Go render tests assert the new
markup (`srcset`, name-tag strip, slideshow chrome). Behavior is checked in a
real browser at phone and iPad viewport sizes. The results report what was and
was not verified.

## Risks

- Decoding large JPEGs is CPU and transient-memory heavy (Go has no DCT
  downscale). Mitigated by the decode semaphore, pixel limit and cache.
- The in-memory cache is lost on restart. Browsers cache thumbnails
  (`Cache-Control`), which softens this.
- HEIC photos (iPhone exports) are not thumbnailed and load at full size in the
  grid.
