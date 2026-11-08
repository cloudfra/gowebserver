var CFG = window.GOWS || { sort: 'name', thumbs: false, videoThumbs: false, media: [] };
var doc = document;
var root = doc.documentElement;
var $ = function (id) { return doc.getElementById(id); };
var reducedMotion = matchMedia('(prefers-reduced-motion: reduce)');
// Whether a mouse or trackpad is in use: either it's the main input, or
// (the "any-" half) it's attached to a device whose main input is touch,
// like an iPad with a trackpad. Both are asked, since a browser can get
// either one wrong or not know "any-" at all. The CSS hover rules ask
// the same way.
var finePointer = matchMedia('(hover: hover) and (pointer: fine), (any-hover: hover) and (any-pointer: fine)');
var numFmt = new Intl.NumberFormat();
var dateFmt = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' });
var monthFmt = new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric' });

function store(key, value) {
  try {
    if (value === undefined) return localStorage.getItem(key);
    if (value === null) localStorage.removeItem(key); else localStorage.setItem(key, value);
  } catch (e) { }
  return null;
}

// Same output as go-humanize's Bytes(), which renders sizes in the list.
function fmtBytes(n) {
  if (n < 10) return n + ' B';
  var units = ['B', 'kB', 'MB', 'GB', 'TB', 'PB', 'EB'];
  var e = Math.min(Math.floor(Math.log(n) / Math.log(1000)), units.length - 1);
  var v = n / Math.pow(1000, e);
  return (v < 10 ? v.toFixed(1) : v.toFixed(0)) + ' ' + units[e];
}

function plural(n, one, many) {
  return numFmt.format(n) + ' ' + (n === 1 ? one : many);
}

function svgUse(id, cls) {
  return '<svg' + (cls ? ' class="' + cls + '"' : '') + ' viewBox="0 0 24 24"><use href="#' + id + '"/></svg>';
}

// Thumbnails come in a few sizes, each double the last, so each file
// is rendered in as few sizes as possible and the browser never
// scales one by more than about 2x. They cover the tile's box (square
// for photos, 16:9 for videos) at the file's own shape (fit=outside),
// and the tile crops them (object-fit: cover), so scaling never
// distorts them and the image still has the file's proportions: the
// zoom into the slideshow flies it to the photo's real shape.
// The size for each kind of tile is picked for this screen's pixel
// density and only ever grows, so resizing the window doesn't fetch
// a new set.
var THUMB_SIZES = [128, 256, 512, 1024];
var THUMB_DPR = Math.min(window.devicePixelRatio || 1, 2);
var tileThumb = { photo: 0, video: 0 };
function hasThumb(item) {
  return CFG.thumbs && (!item.video || CFG.videoThumbs);
}
// URL of the thumbnail for item's grid tile, cssW CSS pixels wide.
// A cssW of 0 gives the size the grid already uses, so the filmstrip
// reuses the grid's thumbnails from the browser cache. Falls back to
// the original file when the server can't make thumbnails.
function tileThumbURL(item, cssW) {
  if (!hasThumb(item)) return item.url;
  var kind = item.video ? 'video' : 'photo';
  // Scaling up a little looks fine, and saves a size step.
  var want = cssW * THUMB_DPR / 1.15;
  var s = THUMB_SIZES[THUMB_SIZES.length - 1];
  for (var i = 0; i < THUMB_SIZES.length; i++) {
    if (THUMB_SIZES[i] >= want) { s = THUMB_SIZES[i]; break; }
  }
  s = tileThumb[kind] = Math.max(tileThumb[kind], s);
  var h = item.video ? Math.round(s * 9 / 16) : s;
  return item.url + '?width=' + s + '&height=' + h + '&fit=outside';
}
// URL of the slideshow's preview of item, shown until a sharper
// version loads: one size for every screen, at the file's own ratio.
var PREVIEW_PX = 1024;
function previewURL(item) {
  if (!hasThumb(item)) return item.url;
  return item.url + '?width=' + PREVIEW_PX + '&height=' + PREVIEW_PX + '&fit=inside';
}

// TEMPORARY diagnostics (see diagnostics.go): with the server's -debug,
// events go to it in batches. The server anonymizes every URL and keeps
// only known fields.
var diag = (function () {
  if (!CFG.diag) return function () { };
  try { performance.setResourceTimingBufferSize(20000); } catch (e) { }
  var buf = [], t0 = performance.now(), timer = 0;
  function flush() {
    clearTimeout(timer);
    timer = 0;
    if (!buf.length) return;
    var body = buf.map(function (o) { return JSON.stringify(o); }).join('\n');
    buf = [];
    try {
      if (!navigator.sendBeacon(CFG.diag, new Blob([body], { type: 'text/plain' }))) {
        fetch(CFG.diag, { method: 'POST', body: body, keepalive: true });
      }
    } catch (e) { }
  }
  addEventListener('pagehide', flush);
  // A script error's kind and where, never its message, which can quote
  // a name: its line counted from this one, which is the same in every
  // page whatever the folder holds above the scripts, and the names of
  // the functions on its stack, which are the scripts' own.
  var base = (function () {
    try { throw new Error(); } catch (e) { return stackLine(e.stack); }
  })();
  function stackLine(stack) {
    var m = /:(\d+):\d+\)?\s*$/m.exec(String(stack || '').split('\n').filter(function (l) { return /:\d+:\d+/.test(l); })[0] || '');
    return m ? +m[1] : 0;
  }
  function stackNames(stack) {
    return String(stack || '').split('\n').map(function (l) {
      var m = /^\s*at (?:new )?([A-Za-z_$][\w$.]*) /.exec(l) || /^([A-Za-z_$][\w$.]*)@/.exec(l);
      return m ? m[1] : (/^\s*at /.test(l) || /@/.test(l) ? 'anonymous' : '');
    }).filter(Boolean).slice(0, 8).join('<');
  }
  addEventListener('error', function (e) {
    if (!e.error && !e.lineno) return;
    buf.push({
      ev: 'js-error', name: e.error && e.error.name, line: base ? e.lineno - base : e.lineno, col: e.colno,
      stack: e.error ? stackNames(e.error.stack) : '', pt: Math.round(performance.now() - t0)
    });
  });
  doc.addEventListener('visibilitychange', function () { if (doc.visibilityState === 'hidden') flush(); });
  return function (ev, o) {
    o = o || {};
    o.ev = ev;
    o.pt = Math.round(performance.now() - t0);
    buf.push(o);
    if (buf.length > 300) flush(); else if (!timer) timer = setTimeout(flush, 2000);
  };
})();
// What the browser knows of a fetched URL: status, size, server timing.
function diagResource(url) {
  try {
    var es = performance.getEntriesByName(new URL(url, location.href).href);
    var e = es[es.length - 1];
    if (!e) return {};
    return {
      status: e.responseStatus, bytes: e.encodedBodySize, cache: e.transferSize === 0 ? 'hit' : 'net',
      timing: (e.serverTiming || []).map(function (s) { return s.name + ':' + Math.round(s.duration); }).join(',')
    };
  } catch (e) { return {}; }
}
