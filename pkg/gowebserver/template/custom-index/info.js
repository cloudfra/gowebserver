// ===== The info panel =====
// What the slideshow knows of the current photo or video, read from the
// file itself by the server (NAME.EXT?info, mediainfo.go): fetched only
// while the panel is open, for the slide on screen, and kept for the
// last few. It's ranked as the server ranks it:
//
//   1. where it was taken: a map with a pin, the place and country, the
//      coordinates, altitude, direction and accuracy;
//   2. when it was taken (or recorded, or released);
//   3. the file's name;
//   4. its size, with the picture's dimensions and the length;
//   5. text the file carries: lyrics, a description, a comment;
//   6. behind "More details": the camera and lens, the exposure, the
//      song, the video and audio streams, the color (model, bit depth,
//      color space, ICC profile, gamma), the description and the file
//      format, and then every tag read, as the file has it;
//   7. "All metadata": everything the file holds, raw and structured,
//      in a view with room for it (metaview.js).
//
// The map is drawn here from the server's own world map (?worldmap,
// fetched the first time a place is shown), so where a photo was taken
// never leaves the server; the OpenStreetMap link is the viewer's to
// follow. On a wide stage the panel docks at the right and the slide
// makes room for it (room()); on a narrow one it's a sheet over the
// bottom of the screen.
function InfoPanel(opts) {
  opts = opts || {};
  var panel = $('ss-panel'), body = $('ss-panel-body'), btn = $('ss-info');
  var dlg = $('ss');
  var on = false, sheet = false, cur = null, token = 0, timer = 0, ctrl = null;
  var cache = new Map(), CACHE_MAX = 60;
  // The map's zoom: the width it shows, in degrees of longitude.
  // The outlines are too coarse to show more than a region: closer in,
  // the OpenStreetMap link is the way.
  var SPANS = [360, 48, 12], level = 2;
  var moreOpen = false, tagsOpen = false;
  // The document on show, and the viewer of all of it (metaview.js),
  // made the first time it's asked for.
  var shown = null, viewer = null;
  var WIDTH = 360, GAP = 14, DOCK_MIN_W = 900, DOCK_MIN_H = 480;
  var FETCH_AFTER = 120;

  // ---- Layout ----
  // The stage's size decides whether the panel docks or is a sheet, and
  // how tall the sheet is: a little over half the stage, so the photo
  // still shows above it.
  var sheetH = 0;
  function fit(w, h) {
    sheet = w < DOCK_MIN_W || h < DOCK_MIN_H;
    sheetH = Math.min(Math.round(h * 0.56), 520);
    sync();
  }
  // The room a docked panel takes at the stage's right edge, and a sheet
  // at its bottom.
  function room() {
    return on && !sheet ? WIDTH + 2 * GAP : 0;
  }
  function roomBelow() {
    return on && sheet ? sheetH : 0;
  }
  function sync() {
    dlg.classList.toggle('info-on', on);
    dlg.classList.toggle('info-sheet', sheet);
    dlg.classList.toggle('info-docked', on && !sheet);
    dlg.style.setProperty('--ssi-room', room() + 'px');
    if (sheetH) dlg.style.setProperty('--ssi-sheet', sheetH + 'px');
    btn.setAttribute('aria-expanded', String(on));
    btn.setAttribute('aria-label', on ? 'Hide details' : 'Details');
    panel.setAttribute('aria-hidden', String(!on));
    if ('inert' in panel) panel.inert = !on;
  }

  // ---- Open and close ----
  function open() {
    if (on) return;
    on = true;
    sync();
    if (opts.layout) opts.layout();
    if (cur) load(cur, true);
  }
  // quiet: the slideshow is closing, so the focus has somewhere to go.
  function close(quiet) {
    if (viewer) viewer.close();
    if (!on) return;
    var inside = panel.contains(doc.activeElement);
    on = false;
    abort();
    sync();
    if (!quiet && opts.layout) opts.layout();
    if (inside && !quiet) (btn.offsetParent !== null ? btn : dlg).focus();
  }
  function toggle() {
    if (on) close(); else open();
  }

  btn.addEventListener('click', toggle);
  $('ss-panel-close').addEventListener('click', function () { close(); });
  body.addEventListener('toggle', function (e) {
    if (e.target.classList.contains('ssi-more')) moreOpen = e.target.open;
    if (e.target.classList.contains('ssi-tags')) tagsOpen = e.target.open;
  }, true);
  body.addEventListener('click', function (e) {
    var all = e.target.closest && e.target.closest('.ssi-all');
    if (all) { openAll(all); return; }
    var z = e.target.closest && e.target.closest('[data-zoom]');
    if (!z) return;
    level = Math.max(0, Math.min(SPANS.length - 1, level + Number(z.getAttribute('data-zoom'))));
    var m = body.querySelector('.ssi-map');
    if (m && m._loc) drawMap(m, m._loc, m._country);
  });

  // ---- The current slide ----
  function show(it) {
    cur = it;
    if (on) load(it, false);
  }

  function abort() {
    clearTimeout(timer);
    if (ctrl) { try { ctrl.abort(); } catch (e) { } }
    ctrl = null;
  }

  function infoURL(it) {
    return it.url + '?info';
  }

  // Shows what the listing already says at once, then the file's own
  // details: right away when they're kept, else a moment later, so
  // flipping quickly through a folder doesn't ask for each slide passed.
  function load(it, now) {
    abort();
    var t = ++token;
    var kept = cache.get(it.url);
    if (kept) {
      render(it, kept, 'ok');
      return;
    }
    render(it, null, 'loading');
    timer = setTimeout(function () { fetchInfo(it, t); }, now ? 0 : FETCH_AFTER);
  }

  function fetchInfo(it, t) {
    if (typeof fetch !== 'function') return;
    ctrl = typeof AbortController === 'function' ? new AbortController() : null;
    var start = performance.now();
    fetch(infoURL(it), ctrl ? { signal: ctrl.signal } : undefined).then(function (r) {
      if (!r.ok) throw new Error('status ' + r.status);
      return r.json();
    }).then(function (d) {
      diag('info', { ok: true, ms: Math.round(performance.now() - start) });
      if (cache.size >= CACHE_MAX) cache.delete(cache.keys().next().value);
      cache.set(it.url, d);
      if (t === token && on) render(it, d, 'ok');
    }, function (e) {
      if (e && e.name === 'AbortError') return;
      diag('info', { ok: false, ms: Math.round(performance.now() - start) });
      if (t === token && on) render(it, null, 'error');
    });
  }

  // ---- Rendering ----
  function render(it, d, state) {
    var h = '';
    var loc = d && d.location, place = d && d.place;
    if (loc || place) h += locationHTML(loc, place);
    h += factsHTML(it, d);
    if (state === 'loading') {
      h += '<div class="ssi-skel" aria-hidden="true"><i></i><i></i><i></i></div>';
    } else if (state === 'error') {
      h += '<p class="ssi-note">Details from the file itself are unavailable.</p>';
    }
    if (d && d.text) d.text.forEach(function (x, i) { h += textHTML(x, i); });
    if (d && ((d.groups && d.groups.length) || (d.tags && d.tags.length))) h += moreHTML(d);
    if (d && ((d.raw && d.raw.length) || (d.structures && d.structures.length))) h += allHTML(d);
    shown = d;
    body.innerHTML = h;
    body.setAttribute('aria-busy', String(state === 'loading'));
    if (loc) {
      var m = body.querySelector('.ssi-map');
      m._loc = loc;
      m._country = loc.country || '';
      drawMap(m, loc, m._country);
    }
  }

  function row(label, value, extra, cls) {
    return '<div class="ssi-fact' + (cls ? ' ' + cls : '') + '"><dt>' + label + '</dt><dd>' + value +
      (extra ? '<span class="ssi-dim">' + extra + '</span>' : '') + '</dd></div>';
  }

  function factsHTML(it, d) {
    var kind = d ? d.kind : (it.video ? 'video' : 'image');
    var h = '<dl class="ssi-facts">';
    var when = d && d.taken ? fmtTaken(d.taken) : null;
    if (when) {
      h += row({ image: 'Taken', video: 'Recorded', audio: 'Released' }[kind] || 'Date', escapeHTML(when.text), when.zone ? escapeHTML(when.zone) : '');
    } else {
      h += row('Modified', escapeHTML(fmtWhen(new Date(it.t * 1000))));
    }
    h += row('Name', escapeHTML(it.name), '', 'ssi-name');
    var extra = [];
    if (d && d.width && d.height) {
      extra.push(numFmt.format(d.width) + ' × ' + numFmt.format(d.height));
      if (kind === 'image') extra.push(mp(d.width * d.height) + ' MP');
    }
    if (d && d.duration) extra.push(fmtLength(d.duration));
    h += row('Size', escapeHTML(fmtBytes(d ? d.size : it.size)), escapeHTML(extra.join(' · ')));
    return h + '</dl>';
  }

  function mp(px) {
    var v = px / 1e6;
    return v >= 10 ? String(Math.round(v)) : String(Math.round(v * 10) / 10);
  }

  function locationHTML(loc, place) {
    var h = '<section class="ssi-loc" aria-label="Location">';
    var name = [place, loc && loc.country && (!place || place.indexOf(loc.country) < 0) ? loc.country : ''].filter(Boolean).join(', ');
    if (loc) {
      h += '<figure class="ssi-map" role="img" aria-label="' + escapeHTML('Map: ' + (name || coords(loc))) + '">' +
        '<div class="ssi-map-art"></div><span class="ssi-pin" aria-hidden="true"></span>' +
        '<div class="ssi-zoom"><button type="button" class="ssi-zbtn" data-zoom="1" aria-label="Zoom in on the map">+</button>' +
        '<button type="button" class="ssi-zbtn" data-zoom="-1" aria-label="Zoom out on the map">−</button></div></figure>';
    }
    h += '<div class="ssi-place">';
    if (name) h += '<div class="ssi-where">' + escapeHTML(name) + '</div>';
    if (loc) {
      var bits = [coords(loc)];
      if (loc.alt != null) bits.push(numFmt.format(Math.round(loc.alt)) + ' m ' + (loc.alt < 0 ? 'below' : 'above') + ' sea level');
      if (loc.direction != null) bits.push('facing ' + compass(loc.direction) + ' (' + Math.round(loc.direction) + '°' + (loc.directionRef === 'M' ? ' magnetic' : '') + ')');
      if (loc.accuracy) bits.push('± ' + numFmt.format(Math.round(loc.accuracy)) + ' m');
      h += '<div class="ssi-coords">' + bits.map(escapeHTML).join('<span class="ssi-sep" aria-hidden="true"> · </span>') + '</div>';
      h += '<a class="ssi-osm" href="' + escapeHTML(osmURL(loc)) + '" target="_blank" rel="noopener noreferrer">Open in OpenStreetMap' +
        '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M14 4h6v6M20 4l-9 9"/></svg></a>';
    }
    return h + '</div></section>';
  }

  function coords(loc) {
    return Math.abs(loc.lat).toFixed(5) + '° ' + (loc.lat < 0 ? 'S' : 'N') + ', ' + Math.abs(loc.lon).toFixed(5) + '° ' + (loc.lon < 0 ? 'W' : 'E');
  }

  function osmURL(loc) {
    var lat = loc.lat.toFixed(6), lon = loc.lon.toFixed(6);
    return 'https://www.openstreetmap.org/?mlat=' + lat + '&mlon=' + lon + '#map=16/' + lat + '/' + lon;
  }

  function compass(deg) {
    var names = ['north', 'north-east', 'east', 'south-east', 'south', 'south-west', 'west', 'north-west'];
    return names[Math.round((((deg % 360) + 360) % 360) / 45) % 8];
  }

  function textHTML(x, i) {
    var id = 'ssi-text-' + i;
    var text = x.synced ? x.text.split('\n').map(function (l) {
      var m = /^\[([0-9:.]+)\] ?(.*)$/.exec(l);
      return m ? '<span class="ssi-ts">' + escapeHTML(m[1]) + '</span>' + escapeHTML(m[2]) : escapeHTML(l);
    }).join('\n') : escapeHTML(x.text);
    return '<section class="ssi-text" aria-labelledby="' + id + '"><h3 id="' + id + '">' + escapeHTML(x.label) +
      (x.lang ? ' <span class="ssi-lang">' + escapeHTML(x.lang) + '</span>' : '') + '</h3>' +
      '<div class="ssi-prose' + (x.synced ? ' synced' : '') + '" tabindex="0">' + text + '</div></section>';
  }

  // A line under "More details" of what's in it, so it's worth opening.
  function teaser(d) {
    var pick = function (g, label) {
      var v = '';
      (d.groups || []).forEach(function (x) {
        if (x.title === g) x.fields.forEach(function (f) { if (!v && f.label === label) v = f.value; });
      });
      return v;
    };
    var parts = [pick('Camera', 'Camera'), pick('Exposure', 'Aperture'), pick('Exposure', 'Shutter speed'),
      pick('Exposure', 'ISO') && 'ISO ' + pick('Exposure', 'ISO'), pick('Song', 'Artist'), pick('Video', 'Codec').split(',')[0]];
    var t = parts.filter(Boolean).slice(0, 4).join(' · ');
    return t || d.format || '';
  }

  function moreHTML(d) {
    var h = '<details class="ssi-more"' + (moreOpen ? ' open' : '') + '><summary><span class="ssi-sum">' +
      '<span class="ssi-sum-t">More details</span><span class="ssi-dim">' + escapeHTML(teaser(d)) + '</span></span>' +
      '<svg class="ssi-chev" viewBox="0 0 24 24" aria-hidden="true"><polyline points="9 6 15 12 9 18"/></svg></summary>' +
      '<div class="ssi-more-body">';
    (d.groups || []).forEach(function (g) {
      h += '<section class="ssi-group"><h3>' + escapeHTML(g.title) + '</h3><dl class="ssi-facts">';
      g.fields.forEach(function (f) { h += row(escapeHTML(f.label), escapeHTML(f.value)); });
      h += '</dl></section>';
    });
    if (d.tags && d.tags.length) {
      h += '<details class="ssi-tags"' + (tagsOpen ? ' open' : '') + '><summary><span class="ssi-sum-t">All tags</span>' +
        '<span class="ssi-count">' + numFmt.format(d.tags.length) + '</span>' +
        '<svg class="ssi-chev" viewBox="0 0 24 24" aria-hidden="true"><polyline points="9 6 15 12 9 18"/></svg></summary><dl class="ssi-raw">';
      var last = '';
      d.tags.forEach(function (tg) {
        if (tg.group !== last) {
          h += '<div class="ssi-raw-group">' + escapeHTML(tg.group) + '</div>';
          last = tg.group;
        }
        h += '<div><dt>' + escapeHTML(tg.name) + '</dt><dd>' + escapeHTML(tg.value) + '</dd></div>';
      });
      h += '</dl></details>';
    }
    return h + '</div></details>';
  }

  // ---- Dates ----
  var WHEN = { weekday: 'short', year: 'numeric', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' };
  var whenFmt = new Intl.DateTimeFormat(undefined, WHEN);
  var whenUTC = new Intl.DateTimeFormat(undefined, Object.assign({ timeZone: 'UTC' }, WHEN));
  var dayUTC = new Intl.DateTimeFormat(undefined, { weekday: 'short', year: 'numeric', month: 'short', day: 'numeric', timeZone: 'UTC' });

  function fmtWhen(dt) {
    return whenFmt.format(dt);
  }

  // A date as the file wrote it: the camera's own wall clock, with its
  // offset from UTC when it gave one (not moved into the viewer's time
  // zone: a photo taken at sunset in Paris says 18:45 wherever it's
  // looked at); a time in UTC ("Z") in the viewer's time; a year alone.
  function fmtTaken(s) {
    var m = /^(\d{4})-(\d\d)-(\d\d)(?:[T ](\d\d):(\d\d)(?::(\d\d))?(?:\.\d+)?)?(Z|[+-]\d\d:?\d\d)?$/.exec(s);
    if (!m) return { text: s };
    if (m[4] === undefined) return { text: dayUTC.format(new Date(Date.UTC(+m[1], m[2] - 1, +m[3]))) };
    if (m[7] === 'Z') return { text: whenFmt.format(new Date(s)) };
    var wall = new Date(Date.UTC(+m[1], m[2] - 1, +m[3], +m[4], +m[5], +(m[6] || 0)));
    var zone = '';
    if (m[7]) {
      var o = m[7].replace(':', ''), hh = +o.slice(1, 3), mm = +o.slice(3);
      zone = 'UTC' + o[0] + hh + (mm ? ':' + o.slice(3) : '');
    }
    return { text: whenUTC.format(wall), zone: zone };
  }

  function fmtLength(sec) {
    sec = Math.round(sec);
    var h = Math.floor(sec / 3600), m = Math.floor(sec % 3600 / 60), s = sec % 60;
    var pad = function (n) { return (n < 10 ? '0' : '') + n; };
    return h ? h + ':' + pad(m) + ':' + pad(s) : m + ':' + pad(s);
  }

  // ---- The map ----
  var world = null;
  function loadWorld() {
    if (!world) {
      world = fetch('?worldmap').then(function (r) {
        if (!r.ok) throw new Error('status ' + r.status);
        return r.json();
      }).then(decodeWorld);
      world.catch(function () { world = null; });
    }
    return world;
  }

  // Each country's rings as points in degrees, with their bounds.
  function decodeWorld(m) {
    var q = m.q;
    return m.c.map(function (row) {
      return {
        name: row[0],
        rings: row.slice(1).map(function (enc) {
          var pts = new Float32Array(enc.length), x = 0, y = 0;
          var b = [Infinity, Infinity, -Infinity, -Infinity];
          for (var i = 0; i + 1 < enc.length; i += 2) {
            x += enc[i]; y += enc[i + 1];
            var lon = x / q, lat = y / q;
            pts[i] = lon; pts[i + 1] = lat;
            if (lon < b[0]) b[0] = lon;
            if (lat < b[1]) b[1] = lat;
            if (lon > b[2]) b[2] = lon;
            if (lat > b[3]) b[3] = lat;
          }
          return { pts: pts, b: b };
        })
      };
    });
  }

  // The view of the map around loc at the current zoom: a box in
  // degrees, and how much a degree of longitude is narrowed at that
  // latitude (an equirectangular map, true to shape at the pin).
  function mapView(loc, aspect, lvl) {
    var span = SPANS[lvl == null ? level : lvl];
    var k = span >= 360 ? 1 : Math.max(0.2, Math.cos(loc.lat * Math.PI / 180));
    var hw = span / 2, hh = hw * k * aspect;
    var cy = span >= 360 ? Math.max(-90 + hh, Math.min(90 - hh, loc.lat)) : loc.lat;
    return { k: k, x0: loc.lon - hw, x1: loc.lon + hw, y0: cy - hh, y1: cy + hh, cy: cy };
  }

  function drawMap(fig, loc, country) {
    var art = fig.querySelector('.ssi-map-art');
    var zin = fig.querySelector('[data-zoom="1"]'), zout = fig.querySelector('[data-zoom="-1"]');
    zin.disabled = level === SPANS.length - 1;
    zout.disabled = level === 0;
    var t = token;
    loadWorld().then(function (countries) {
      if (t !== token || !fig.isConnected) return;
      var w = fig.clientWidth || WIDTH - 32, hgt = fig.clientHeight || Math.round(w * 0.6);
      var v = mapView(loc, hgt / w);
      art.innerHTML = mapSVG(countries, loc, v, country, w);
      fig.classList.add('drawn');
      // Where the pin goes: the point, which isn't the middle when the
      // world view is held off the poles.
      fig.style.setProperty('--pin-y', ((v.y1 - loc.lat) / (v.y1 - v.y0) * 100) + '%');
    }, function () {
      if (t === token && fig.isConnected) fig.classList.add('no-map');
    });
  }

  function mapSVG(countries, loc, v, country, px, lvl) {
    if (lvl == null) lvl = level;
    var k = v.k, land = '', here = '';
    var shifts = [0];
    if (v.x0 < -180) shifts.push(360);
    if (v.x1 > 180) shifts.push(-360);
    var r2 = function (n) { return Math.round(n * 100) / 100; };
    countries.forEach(function (c) {
      c.rings.forEach(function (r) {
        shifts.forEach(function (s) {
          var b = r.b;
          if (b[2] + s < v.x0 || b[0] + s > v.x1 || b[3] < v.y0 || b[1] > v.y1) return;
          var p = r.pts, d = 'M';
          for (var i = 0; i < p.length; i += 2) d += (i ? 'L' : '') + r2((p[i] + s) * k) + ' ' + r2(-p[i + 1]);
          d += 'Z';
          if (c.name === country) here += d; else land += d;
        });
      });
    });
    var x = v.x0 * k, y = -v.y1, w = (v.x1 - v.x0) * k, h = v.y1 - v.y0;
    // Lines of latitude and longitude, at a step that suits the zoom.
    var step = [30, 10, 2][lvl], grid = '';
    for (var lon = Math.ceil(v.x0 / step) * step; lon <= v.x1; lon += step) grid += 'M' + r2(lon * k) + ' ' + r2(y) + 'V' + r2(y + h);
    for (var lat = Math.ceil(v.y0 / step) * step; lat <= v.y1; lat += step) grid += 'M' + r2(x) + ' ' + r2(-lat) + 'H' + r2(x + w);
    var extra = '', cx = loc.lon * k, cy = -loc.lat, unit = w / px; // map units per pixel
    if (loc.accuracy) {
      var rad = loc.accuracy / 111320;
      if (rad / unit > 3) extra += '<circle class="ssi-acc" cx="' + cx + '" cy="' + cy + '" r="' + rad + '"/>';
    }
    if (loc.direction != null && lvl > 0) {
      // The way the camera faced: a fan from the pin, a fixed size on screen.
      var len = 46 * unit, a = loc.direction * Math.PI / 180, spread = 0.42;
      var p1 = [cx + Math.sin(a - spread) * len, cy - Math.cos(a - spread) * len];
      var p2 = [cx + Math.sin(a + spread) * len, cy - Math.cos(a + spread) * len];
      extra += '<path class="ssi-fov" d="M' + cx + ' ' + cy + 'L' + p1.join(' ') + 'A' + len + ' ' + len + ' 0 0 1 ' + p2.join(' ') + 'Z"/>';
    }
    return '<svg viewBox="' + [x, y, w, h].map(r2).join(' ') + '" preserveAspectRatio="xMidYMid slice" aria-hidden="true">' +
      '<rect class="ssi-sea" x="' + r2(x) + '" y="' + r2(y) + '" width="' + r2(w) + '" height="' + r2(h) + '"/>' +
      '<path class="ssi-grid" d="' + grid + '"/>' +
      '<path class="ssi-land" d="' + land + '"/><path class="ssi-here" d="' + here + '"/>' + extra + '</svg>';
  }

  // Keys that belong to what's focused in the panel, not the slideshow.
  function ownsKey(e) {
    if (viewer && viewer.isOpen()) {
      if (e.key === 'Escape') {
        viewer.close();
        e.preventDefault();
      }
      return true;
    }
    return on && panel.contains(e.target) && /^(Home|End|PageUp|PageDown|ArrowUp|ArrowDown| |Enter)$/.test(e.key);
  }

  // ---- All the metadata ----
  // A line at the panel's end that opens everything the file holds, raw
  // and structured, in a view with room for it.
  function allHTML(d) {
    var parts = [];
    if (d.structures && d.structures.length) parts.push(numFmt.format(d.structures.length) + ' structured');
    if (d.raw && d.raw.length) parts.push(numFmt.format(d.raw.length) + ' raw keys');
    return '<button type="button" class="ssi-all"><span class="ssi-sum"><span class="ssi-sum-t">All metadata</span>' +
      '<span class="ssi-dim">' + escapeHTML(parts.join(' · ')) + '</span></span>' +
      '<svg class="ssi-chev" viewBox="0 0 24 24" aria-hidden="true"><path d="M14 4h6v6M20 4l-9 9M19 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h5"/></svg></button>';
  }

  function openAll(from) {
    if (!shown) return;
    if (!viewer) viewer = MetaView({ host: panel.parentNode, map: smallMap });
    viewer.open(shown, cur ? cur.name : shown.name, from);
  }

  // A map of a place for the viewer, at the closest zoom.
  function smallMap(art, loc) {
    loadWorld().then(function (countries) {
      if (!art.isConnected) return;
      var w = art.clientWidth || 240, hgt = art.clientHeight || Math.round(w * 0.6);
      var lvl = SPANS.length - 1, v = mapView(loc, hgt / w, lvl);
      art.innerHTML = mapSVG(countries, loc, v, '', w, lvl);
      art.parentNode.style.setProperty('--pin-y', ((v.y1 - loc.lat) / (v.y1 - v.y0) * 100) + '%');
      art.parentNode.classList.add('ready');
    }, function () { });
  }

  sync();
  return {
    open: open, close: close, toggle: toggle, show: show, fit: fit, room: room, roomBelow: roomBelow, ownsKey: ownsKey,
    isOpen: function () { return on; },
    viewer: function () { return viewer; },
    _view: mapView, _taken: fmtTaken
  };
}
