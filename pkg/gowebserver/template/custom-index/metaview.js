// ===== The metadata viewer =====
// Everything a file's metadata holds (NAME.EXT?info's raw and
// structures, mediainfo_timed.go on the server), in a view with room
// for it, opened from the info panel: a wide sheet over the slideshow
// on a desktop, the whole screen on a phone. Two tabs:
//
//   - Structure: each structured value (a timed metadata track's
//     message, an SEI message, an XMP packet) as a tree of its keys and
//     their types, objects open and lists folded with their length. A
//     message sent many times is shown once, from its first, with what
//     each changing field did beside it: first and last, how many
//     values, the range and a sparkline for a number.
//   - Raw: every key and value as the file holds it, under where it
//     holds it (a box's path, a track's messages, SEI, XMP), grouped by
//     kind of source, each value copyable, long ones folded.
//
// One search box filters either. Values are drawn by what they look
// like when that's clear (MV_DETECTORS, a table to add to): a place on
// the map, a temperature on a thermometer, a time as a date, a length
// as h:mm:ss, a color as a swatch, a percentage as a bar, a picture as
// itself, a yes or no as a chip, numbers over time as a sparkline. A
// detector only draws what it's sure of; anything else is text.
//
// The mv* functions are the parts with no page in them.

// What kind of JSON value v is.
function mvType(v) {
  if (v === null || v === undefined) return 'null';
  if (Array.isArray(v)) return 'array';
  return typeof v === 'object' ? 'object' : typeof v;
}

// v as a number, when it is one or is text that's only one.
function mvNumber(v) {
  if (typeof v === 'number') return isFinite(v) ? v : null;
  if (typeof v === 'string' && /^\s*-?\d+(\.\d+)?([eE][-+]?\d+)?\s*$/.test(v)) return parseFloat(v);
  return null;
}

// The last part of a key's path ("gps.lat" → "lat", "a[2]" → "a"),
// lower case, for the detectors to read.
function mvLeaf(path) {
  var p = String(path || '').replace(/\[\d+\]$/, '');
  var i = p.lastIndexOf('.');
  return (i >= 0 ? p.slice(i + 1) : p).toLowerCase();
}

// A time in Unix milliseconds from a number that looks like one: in
// seconds, milliseconds, microseconds or nanoseconds since 1970, from
// 2001 to 2100. null otherwise.
function mvEpoch(n) {
  if (n == null || !isFinite(n) || n <= 0) return null;
  var scales = [[1e9, 1000], [1e12, 1], [1e15, 1e-3], [1e18, 1e-6]];
  for (var i = 0; i < scales.length; i++) {
    var lo = scales[i][0], k = scales[i][1];
    if (n >= lo && n < lo * 4.1) return n * k;
  }
  return null;
}

// seconds as 1:02:03, or 2:03 under an hour (with tenths under ten
// seconds' precision when there are any).
function mvDuration(sec) {
  if (sec == null || !isFinite(sec) || sec < 0) return '';
  var whole = Math.floor(sec), h = Math.floor(whole / 3600), m = Math.floor(whole % 3600 / 60), s = whole % 60;
  var p = function (n) { return (n < 10 ? '0' : '') + n; };
  var frac = sec - whole >= 0.05 && sec < 60 ? (Math.round((sec - whole) * 10) / 10).toFixed(1).slice(1) : '';
  return (h ? h + ':' + p(m) : String(m)) + ':' + p(s) + frac;
}

// A temperature in °C as a color: blue at 0 and below, red at 35 and
// above, through purple.
function mvTempColor(c) {
  var f = Math.max(0, Math.min(1, c / 35));
  var lerp = function (a, b) { return Math.round(a + (b - a) * f); };
  return 'rgb(' + lerp(59, 239) + ',' + lerp(130, 68) + ',' + lerp(246, 68) + ')';
}

// A small sparkline of [time, value] points, w by h.
function mvSparkline(series, w, h) {
  if (!series || series.length < 2) return '';
  var t0 = series[0][0], t1 = series[series.length - 1][0], lo = Infinity, hi = -Infinity;
  series.forEach(function (p) { lo = Math.min(lo, p[1]); hi = Math.max(hi, p[1]); });
  var dt = t1 - t0 || 1, dv = hi - lo || 1;
  var pts = series.map(function (p, i) {
    var x = t1 > t0 ? (p[0] - t0) / dt * (w - 2) + 1 : i / (series.length - 1) * (w - 2) + 1;
    var y = h - 1 - (p[1] - lo) / dv * (h - 2);
    return x.toFixed(1) + ',' + y.toFixed(1);
  });
  return '<svg class="mv-spark" viewBox="0 0 ' + w + ' ' + h + '" width="' + w + '" height="' + h + '" aria-hidden="true">' +
    '<polyline points="' + pts.join(' ') + '"/></svg>';
}

var MV_WHEN = new Intl.DateTimeFormat(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit' });
var MV_WHEN_UTC = new Intl.DateTimeFormat(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', second: '2-digit', timeZone: 'UTC' });

// Coordinates under the names they go by.
var MV_LAT = /^(lat|latitude|gpslatitude|gps_latitude)$/;
var MV_LON = /^(lon|lng|long|longitude|gpslongitude|gps_longitude)$/;

// An object's latitude and longitude, when it has both, in range.
function mvLatLon(v) {
  if (mvType(v) !== 'object') return null;
  var lat = null, lon = null;
  Object.keys(v).forEach(function (k) {
    var lk = k.toLowerCase(), n = mvNumber(v[k]);
    if (n == null) return;
    if (MV_LAT.test(lk)) lat = n;
    else if (MV_LON.test(lk)) lon = n;
  });
  if (lat == null || lon == null || Math.abs(lat) > 90 || Math.abs(lon) > 180 || (lat === 0 && lon === 0)) return null;
  return { lat: lat, lon: lon };
}

// ISO 6709 ("+48.8584+002.2945+035/"), in degrees.
function mvISO6709(s) {
  var m = /^([+-]\d{1,2}(?:\.\d+)?)([+-]\d{1,3}(?:\.\d+)?)/.exec(String(s));
  if (!m) return null;
  var lat = parseFloat(m[1]), lon = parseFloat(m[2]);
  return Math.abs(lat) <= 90 && Math.abs(lon) <= 180 ? { lat: lat, lon: lon } : null;
}

// The detectors, in order: the first sure of a value draws it. test(key,
// value, ctx) returns what draw needs, or null; key is the leaf of the
// value's path in lower case, ctx {path, field} with field what the
// value did over time (the server's structure field), if anything.
// draw returns HTML, and may leave the value's text out (text: false).
var MV_DETECTORS = [
  {
    name: 'location',
    test: function (key, v) {
      if (mvType(v) === 'object') return mvLatLon(v);
      if (typeof v === 'string' && /location|6709/.test(key)) return mvISO6709(v);
      return null;
    },
    draw: function (d) {
      return '<figure class="mv-map" data-lat="' + d.lat + '" data-lon="' + d.lon + '" role="img" aria-label="' +
        escapeHTML('Map: ' + d.lat.toFixed(5) + ', ' + d.lon.toFixed(5)) + '"><div class="mv-map-art"></div><span class="mv-pin" aria-hidden="true"></span></figure>' +
        '<span class="mv-dim">' + escapeHTML(Math.abs(d.lat).toFixed(5) + '° ' + (d.lat < 0 ? 'S' : 'N') + ', ' + Math.abs(d.lon).toFixed(5) + '° ' + (d.lon < 0 ? 'W' : 'E')) + '</span>';
    }
  },
  {
    name: 'image',
    test: function (key, v) {
      return typeof v === 'string' && /^data:image\/(png|jpe?g|gif|webp|bmp);base64,[A-Za-z0-9+/=]+$/.test(v) ? v : null;
    },
    draw: function (src) { return '<img class="mv-img" alt="" src="' + escapeHTML(src) + '">'; },
    text: false
  },
  {
    name: 'color',
    test: function (key, v) {
      if (typeof v !== 'string') return null;
      var s = v.trim();
      if (/^#([0-9a-f]{3}|[0-9a-f]{4}|[0-9a-f]{6}|[0-9a-f]{8})$/i.test(s)) return s;
      if (/^rgba?\(\s*\d{1,3}\s*,\s*\d{1,3}\s*,\s*\d{1,3}\s*(,\s*[\d.]+\s*)?\)$/i.test(s)) return s;
      return null;
    },
    draw: function (c) { return '<span class="mv-swatch" style="background:' + escapeHTML(c) + '"></span>'; }
  },
  {
    name: 'temperature',
    test: function (key, v) {
      var n = mvNumber(v), unit = 'C';
      if (n == null && typeof v === 'string') {
        var m = /^\s*(-?\d+(?:\.\d+)?)\s*°?\s*([CF])\s*$/i.exec(v);
        if (!m || (v.indexOf('°') < 0 && !/temp/.test(key))) return null;
        n = parseFloat(m[1]);
        unit = m[2].toUpperCase();
      } else {
        if (n == null || !/temp|celsius|fahrenheit/.test(key)) return null;
        if (/fahrenheit|(^|_)f$|_f_|degf/.test(key)) unit = 'F';
      }
      var c = unit === 'F' ? (n - 32) * 5 / 9 : n;
      return c > -90 && c < 150 ? { c: c, n: n, unit: unit } : null;
    },
    draw: function (d) {
      var f = Math.max(0.06, Math.min(1, (d.c + 20) / 70));
      return '<span class="mv-thermo" title="' + escapeHTML(d.n + ' °' + d.unit) + '" style="--mv-t:' + mvTempColor(d.c) + ';--mv-f:' + f.toFixed(3) + '"><i></i></span>' +
        '<span class="mv-dim">' + escapeHTML((Math.round(d.c * 10) / 10) + ' °C') + '</span>';
    }
  },
  {
    name: 'time',
    test: function (key, v) {
      if (typeof v === 'string' && /^\d{4}-\d\d-\d\d[T ]\d\d:\d\d(:\d\d(\.\d+)?)?(Z|[+-]\d\d:?\d\d)?$/.test(v.trim())) {
        var s = v.trim().replace(' ', 'T'), zoned = /(Z|[+-]\d\d:?\d\d)$/.test(s);
        var d = new Date(zoned ? s : s + 'Z');
        return isNaN(d) ? null : { ms: d.getTime(), utc: !zoned };
      }
      var n = mvNumber(v);
      if (n == null || !/time|date|stamp|epoch|created|updated|modified|utc|clock|_at$|_(s|ms|us|ns)$/.test(key)) return null;
      var ms = mvEpoch(n);
      return ms == null ? null : { ms: ms, utc: false };
    },
    draw: function (d) {
      return '<span class="mv-when">' + escapeHTML((d.utc ? MV_WHEN_UTC : MV_WHEN).format(new Date(d.ms))) + '</span>';
    }
  },
  {
    name: 'duration',
    test: function (key, v) {
      var n = mvNumber(v);
      if (n == null || n < 0 || !/duration|elapsed|uptime|length|runtime|interval/.test(key)) return null;
      if (/(_|^)ms$|millis/.test(key)) n /= 1000;
      else if (/(_|^)us$|micros/.test(key)) n /= 1e6;
      return n;
    },
    draw: function (sec) { return '<span class="mv-when">' + escapeHTML(mvDuration(sec)) + '</span>'; }
  },
  {
    name: 'percent',
    test: function (key, v) {
      var n = mvNumber(v);
      if (n == null) return null;
      if (/ratio|fraction/.test(key) && n >= 0 && n <= 1) return n * 100;
      if (/percent|pct|battery|humidity|charge|%/.test(key) && n >= 0 && n <= 100) return n;
      return null;
    },
    draw: function (p) {
      return '<span class="mv-bar" role="img" aria-label="' + escapeHTML(Math.round(p) + '%') + '"><i style="width:' + p.toFixed(1) + '%"></i></span>';
    }
  },
  {
    name: 'boolean',
    test: function (key, v) { return typeof v === 'boolean' ? { v: v } : null; },
    draw: function (d) { return '<span class="mv-chip ' + (d.v ? 'yes' : 'no') + '">' + (d.v ? 'true' : 'false') + '</span>'; },
    text: false
  },
  {
    name: 'series',
    test: function (key, v, ctx) {
      var f = ctx && ctx.field;
      return f && f.series && f.series.length >= 3 && mvNumber(v) != null ? f : null;
    },
    draw: function (f) { return mvSparkline(f.series, 96, 22); }
  }
];

// What MV_DETECTORS make of a value: [{name, html, text}], the first
// sure of each kind, at most two (a number can be a temperature over
// time: a thermometer and a sparkline).
function mvDetect(path, v, ctx) {
  var key = mvLeaf(path), out = [];
  for (var i = 0; i < MV_DETECTORS.length && out.length < 2; i++) {
    var det = MV_DETECTORS[i], d;
    try { d = det.test(key, v, ctx || {}); } catch (e) { d = null; }
    if (d == null || d === false) continue;
    // A sparkline goes with anything; others are one per value.
    if (out.length && det.name !== 'series') continue;
    out.push({ name: det.name, html: det.draw(d, ctx || {}), text: det.text !== false });
  }
  return out;
}

// Raw metadata ({path, key, value} each) by where it is, in the order
// first found: [{path, rows}].
function mvRawGroups(raw) {
  var out = [], at = new Map();
  (raw || []).forEach(function (r) {
    var g = at.get(r.path);
    if (!g) { g = { path: r.path, rows: [] }; at.set(r.path, g); out.push(g); }
    g.rows.push(r);
  });
  return out;
}

// The kind of source a raw path is: XMP, SEI, a timed metadata track,
// or the file's boxes.
var MV_SOURCES = [['boxes', 'Boxes'], ['timed', 'Timed metadata'], ['sei', 'SEI'], ['xmp', 'XMP']];
function mvSourceOf(path) {
  var p = String(path || '');
  if (/XMP/i.test(p)) return 'xmp';
  if (/^SEI /.test(p)) return 'sei';
  if (/\/(amf0|amf3|mett|metx|tx3g|text|stpp|wvtt)( |$)/.test(p)) return 'timed';
  return 'boxes';
}

// Whether an entry matches a filter: every word of it is in the text,
// whatever the case.
function mvMatch(q, text) {
  var words = String(q || '').toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return true;
  var hay = String(text).toLowerCase();
  return words.every(function (w) { return hay.indexOf(w) >= 0; });
}

// A value long enough to fold to a few lines.
var MV_LONG = 160;

// opts.host is where the viewer goes (the slideshow's dialog); opts.map
// (figure, {lat, lon}) draws a map in a figure; opts.closed is called
// when it closes.
function MetaView(opts) {
  opts = opts || {};
  var el = doc.createElement('div');
  el.className = 'mv';
  el.hidden = true;
  el.setAttribute('role', 'dialog');
  el.setAttribute('aria-modal', 'true');
  el.setAttribute('aria-label', 'All metadata');
  el.innerHTML =
    '<div class="mv-sheet">' +
    '<header class="mv-head"><div class="mv-title"><h2>All metadata</h2><span class="mv-name"></span></div>' +
    '<div class="mv-tabs" role="tablist">' +
    '<button type="button" role="tab" class="mv-tab" data-tab="structure" aria-selected="true">Structure <span class="mv-count"></span></button>' +
    '<button type="button" role="tab" class="mv-tab" data-tab="raw" aria-selected="false">Raw <span class="mv-count"></span></button></div>' +
    '<input class="mv-find" type="search" placeholder="Filter keys and values" aria-label="Filter the metadata" autocomplete="off" spellcheck="false">' +
    '<button type="button" class="mv-close" aria-label="Close"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg></button></header>' +
    '<div class="mv-body" tabindex="-1"><div class="mv-pane" data-pane="structure" role="tabpanel"></div><div class="mv-pane" data-pane="raw" role="tabpanel" hidden></div>' +
    '<p class="mv-none" hidden>Nothing matches.</p></div>' +
    '</div>';
  (opts.host || doc.body).appendChild(el);
  var q = function (s) { return el.querySelector(s); };
  var find = q('.mv-find'), nameEl = q('.mv-name'), body = q('.mv-body');
  var panes = { structure: q('[data-pane="structure"]'), raw: q('[data-pane="raw"]') };
  var tabs = el.querySelectorAll('.mv-tab');
  var tab = 'structure', data = null, back = null;

  function open(d, name, from) {
    data = d || {};
    back = from || null;
    nameEl.textContent = name || data.name || '';
    var nStruct = (data.structures || []).length, nRaw = (data.raw || []).length;
    tabs[0].querySelector('.mv-count').textContent = numFmt.format(nStruct);
    tabs[1].querySelector('.mv-count').textContent = numFmt.format(nRaw);
    panes.structure.innerHTML = structuresHTML(data.structures || []);
    panes.raw.innerHTML = rawHTML(data.raw || []);
    find.value = '';
    setTab(nStruct ? 'structure' : 'raw');
    el.hidden = false;
    el.classList.add('open');
    drawMaps();
    try { find.focus({ preventScroll: true }); } catch (e) { find.focus(); }
  }

  function close() {
    if (el.hidden) return;
    el.hidden = true;
    el.classList.remove('open');
    if (back && back.focus) back.focus();
    if (opts.closed) opts.closed();
  }

  function setTab(t) {
    tab = t;
    tabs.forEach(function (b) { b.setAttribute('aria-selected', String(b.getAttribute('data-tab') === t)); });
    panes.structure.hidden = t !== 'structure';
    panes.raw.hidden = t !== 'raw';
    filter(find.value);
    drawMaps();
  }

  function drawMaps() {
    if (!opts.map) return;
    panes[tab].querySelectorAll('.mv-map:not(.drawn)').forEach(function (fig) {
      if (fig.closest('[hidden]')) return;
      fig.classList.add('drawn');
      opts.map(fig.querySelector('.mv-map-art'), { lat: parseFloat(fig.getAttribute('data-lat')), lon: parseFloat(fig.getAttribute('data-lon')) });
    });
  }

  // ---- Structure ----
  function structuresHTML(list) {
    if (!list.length) return '<p class="mv-empty">No structured metadata.</p>';
    return list.map(function (s, i) {
      var meta = [];
      if (s.count > 1) meta.push(numFmt.format(s.count) + ' messages');
      if (s.timed) meta.push(s.count > 1 ? vpFmtTime(s.start) + '–' + vpFmtTime(s.end) : 'at ' + vpFmtTime(s.start));
      return '<section class="mv-struct" data-i="' + i + '"><header><code class="mv-src">' + escapeHTML(s.source) + '</code>' +
        (s.name ? '<span class="mv-sname">' + escapeHTML(s.name) + '</span>' : '') +
        (meta.length ? '<span class="mv-dim">' + escapeHTML(meta.join(' · ')) + '</span>' : '') + '</header>' +
        '<ul class="mv-tree" role="tree">' + nodeHTML('', '', s.value, s.fields || {}, 0, true) + '</ul></section>';
    }).join('');
  }

  // A node of the tree: key, type and value, or its children.
  function nodeHTML(path, key, v, fields, depth, root) {
    var t = mvType(v), field = fields[path];
    var label = root ? '' : '<span class="mv-k">' + escapeHTML(key) + '</span>';
    var dets = mvDetect(path, v, { path: path, field: field });
    var rich = dets.map(function (d) { return d.html; }).join('');
    if (t === 'object' || t === 'array') {
      var keys = t === 'array' ? v.map(function (_, i) { return i; }) : Object.keys(v).sort();
      if (root && t === 'object') {
        return (rich ? '<li class="mv-node mv-rich-row">' + rich + '</li>' : '') + keys.map(function (k) {
          return nodeHTML(k, k, v[k], fields, depth + 1, false);
        }).join('');
      }
      var shown = keys.slice(0, 500), more = keys.length - shown.length;
      var kids = shown.map(function (k) {
        var p = t === 'array' ? path + '[' + k + ']' : (path ? path + '.' + k : String(k));
        return nodeHTML(p, t === 'array' ? '[' + k + ']' : k, v[k], fields, depth + 1, false);
      }).join('') + (more > 0 ? '<li class="mv-more">' + numFmt.format(more) + ' more</li>' : '');
      // A list of plain values reads on one line; its changes, if any,
      // are the list's.
      var plain = t === 'array' && v.length <= 12 && v.every(function (x) { return mvType(x) !== 'object' && mvType(x) !== 'array'; });
      var open = t === 'object' ? depth <= 3 : false;
      return '<li class="mv-node" role="treeitem"><details' + (open ? ' open' : '') + '><summary>' + label +
        '<span class="mv-t">' + (t === 'array' ? '[' + numFmt.format(v.length) + ']' : '{' + numFmt.format(keys.length) + '}') + '</span>' +
        (plain ? '<span class="mv-v mv-inline">' + escapeHTML(JSON.stringify(v)) + '</span>' : '') +
        (rich ? '<span class="mv-rich">' + rich + '</span>' : '') + changeHTML(field) + '</summary>' +
        '<ul role="group">' + kids + '</ul></details></li>';
    }
    var text = t === 'string' ? v : String(v);
    var showText = dets.every(function (d) { return d.text; });
    var long = text.length > MV_LONG;
    return '<li class="mv-node mv-leaf" role="treeitem">' + label + '<span class="mv-t mv-' + t + '">' + t + '</span>' +
      (rich ? '<span class="mv-rich">' + rich + '</span>' : '') +
      (showText ? '<span class="mv-v' + (long ? ' mv-long' : '') + '">' + escapeHTML(text) + '</span>' : '') +
      (long && showText ? '<button type="button" class="mv-fold" aria-expanded="false">Show all</button>' : '') +
      changeHTML(field) + '</li>';
  }

  // What a field did over its messages: first → last, how many values,
  // and its range.
  function changeHTML(f) {
    if (!f) return '';
    var bits = [];
    if (f.min != null && f.max != null) bits.push(numFmt.format(f.min) + ' to ' + numFmt.format(f.max));
    else bits.push(f.first + ' → ' + f.last);
    bits.push(f.values > 1000 ? 'over 1,000 values' : numFmt.format(f.values) + ' values');
    return '<span class="mv-change">' + escapeHTML(bits.join(' · ')) + '</span>';
  }

  // ---- Raw ----
  function rawHTML(raw) {
    if (!raw.length) return '<p class="mv-empty">No raw metadata.</p>';
    var bySource = {};
    mvRawGroups(raw).forEach(function (g) {
      var s = mvSourceOf(g.path);
      (bySource[s] = bySource[s] || []).push(g);
    });
    var h = '';
    MV_SOURCES.forEach(function (src) {
      var groups = bySource[src[0]];
      if (!groups) return;
      var n = groups.reduce(function (a, g) { return a + g.rows.length; }, 0);
      h += '<section class="mv-source" data-source="' + src[0] + '"><h3>' + escapeHTML(src[1]) + ' <span class="mv-count">' + numFmt.format(n) + '</span></h3>';
      groups.forEach(function (g) {
        h += '<details class="mv-box" open><summary><code class="mv-path">' + escapeHTML(g.path) + '</code><span class="mv-count">' +
          numFmt.format(g.rows.length) + '</span></summary><dl class="mv-rows">';
        g.rows.forEach(function (r) {
          var v = String(r.value), long = v.length > MV_LONG || v.indexOf('\n') >= 0;
          h += '<div class="mv-row"><dt>' + escapeHTML(r.key) + '</dt><dd><span class="mv-v' + (long ? ' mv-long' : '') + '">' + escapeHTML(v) + '</span>' +
            (long ? '<button type="button" class="mv-fold" aria-expanded="false">Show all</button>' : '') +
            '<button type="button" class="mv-copy" aria-label="Copy the value">Copy</button></dd></div>';
        });
        h += '</dl></details>';
      });
      h += '</section>';
    });
    return h;
  }

  // ---- Filtering ----
  function filter(query) {
    var any = false;
    if (tab === 'raw') {
      panes.raw.querySelectorAll('.mv-box').forEach(function (box) {
        var path = box.querySelector('.mv-path').textContent, shown = 0;
        box.querySelectorAll('.mv-row').forEach(function (row) {
          var ok = mvMatch(query, path + '\n' + row.querySelector('dt').textContent + '\n' + row.querySelector('.mv-v').textContent);
          row.hidden = !ok;
          if (ok) shown++;
        });
        box.hidden = !shown;
        if (shown && query) box.open = true;
        if (shown) any = true;
      });
      panes.raw.querySelectorAll('.mv-source').forEach(function (s) {
        s.hidden = Array.prototype.every.call(s.querySelectorAll('.mv-box'), function (b) { return b.hidden; });
      });
    } else {
      panes.structure.querySelectorAll('.mv-struct').forEach(function (s) {
        var ok = mvMatch(query, s.textContent);
        s.hidden = !ok;
        if (ok) any = true;
      });
      if (!panes.structure.querySelector('.mv-struct')) any = true;
    }
    q('.mv-none').hidden = any || !query;
  }

  find.addEventListener('input', function () { filter(find.value); });
  tabs.forEach(function (b) { b.addEventListener('click', function () { setTab(b.getAttribute('data-tab')); }); });
  q('.mv-close').addEventListener('click', close);
  el.addEventListener('click', function (e) {
    if (e.target === el) { close(); return; }
    var fold = e.target.closest && e.target.closest('.mv-fold');
    if (fold) {
      var v = fold.parentNode.querySelector('.mv-v'), full = !v.classList.contains('full');
      v.classList.toggle('full', full);
      fold.textContent = full ? 'Show less' : 'Show all';
      fold.setAttribute('aria-expanded', String(full));
      return;
    }
    var copy = e.target.closest && e.target.closest('.mv-copy');
    if (copy) {
      var text = copy.parentNode.querySelector('.mv-v').textContent;
      var done = function () { copy.textContent = 'Copied'; setTimeout(function () { copy.textContent = 'Copy'; }, 1500); };
      // Without the clipboard's API (or its permission), the old way: a
      // selected text area and the copy command.
      var fallback = function () {
        var ta = doc.createElement('textarea');
        ta.value = text;
        ta.setAttribute('readonly', '');
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        el.appendChild(ta);
        ta.select();
        var ok = false;
        try { ok = doc.execCommand('copy'); } catch (err) { ok = false; }
        ta.remove();
        copy.focus();
        if (ok) done();
      };
      if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(text).then(done, fallback);
      else fallback();
    }
  });
  // Its keys are its own: Escape closes it, and nothing reaches the
  // slideshow.
  el.addEventListener('keydown', function (e) {
    e.stopPropagation();
    if (e.key === 'Escape') {
      e.preventDefault();
      close();
    }
  });
  el.addEventListener('toggle', function () { drawMaps(); }, true);

  return {
    el: el, open: open, close: close,
    isOpen: function () { return !el.hidden; },
    tab: function () { return tab; },
    setTab: setTab
  };
}
