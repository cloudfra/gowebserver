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

// Same output as go-humanize's Bytes(), which the server used for sizes:
// two significant digits, rounded once (9950 B is 10 kB, not 9.9 or
// 10.0), with the decimal dropped when rounding carries into a digit.
function fmtBytes(n) {
  if (n < 10) return n + ' B';
  var units = ['B', 'kB', 'MB', 'GB', 'TB', 'PB', 'EB'];
  var e = Math.min(Math.floor(Math.log(n) / Math.log(1000)), units.length - 1);
  var v = n / Math.pow(1000, e);
  var digits = function (x) { return String(Math.trunc(x)).length; };
  var whole = digits(v), d = Math.max(2 - whole, 0), r = Math.pow(10, d);
  v = Math.floor(v * r + 0.5) / r;
  d = Math.max(d - Math.max(digits(v) - whole, 0), 0);
  return v.toFixed(d) + ' ' + units[e];
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
