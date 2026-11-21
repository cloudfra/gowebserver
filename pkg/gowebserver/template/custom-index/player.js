// ===== Video player =====
// The slideshow plays a video with this dock instead of the browser's
// own controls: play, time, volume, speed, captions and full screen, and
// a timeline that shows the video's metadata (videometa.go on the
// server): its chapters as segments, its markers as ticks (or bands, for
// a stretch of time) in their group's color, what's buffered, and on
// hover or drag a preview frame with the time and the nearest chapter or
// marker. The metadata is asked for (VIDEO?meta) only when a video
// becomes the current slide, never for the listing, and once per video
// per page. There is one dock, which the slideshow moves into the
// current slide over its video (attach), so it moves with a swipe and
// sits where the browser's controls would. Every URL is the video's own,
// or one the metadata gives relative to it, so the page works wherever
// the folder is mounted.
//
// The vp* functions are the parts with no page in them: the timeline's
// layout, what's under the pointer, the keyboard model.

var VP_RATES = [0.25, 0.5, 0.75, 1, 1.25, 1.5, 1.75, 2];
// Marker groups without a color of their own take these, in order.
var VP_COLORS = ['#60a5fa', '#f87171', '#4ade80', '#fbbf24', '#c084fc', '#22d3ee'];

// sec as 4:05, or 1:02:13 past an hour (or when long says the video
// lasts that long).
function vpFmtTime(sec, long) {
  sec = Math.max(0, Math.floor(isFinite(sec) ? sec : 0));
  var h = Math.floor(sec / 3600), m = Math.floor(sec % 3600 / 60), s = sec % 60;
  var p = function (n) { return n < 10 ? '0' + n : '' + n; };
  return h || long ? h + ':' + p(m) + ':' + p(s) : m + ':' + p(s);
}

// A time written as 1:02:13, 62:13, 3733, 3733.5 or 1h2m13s, in seconds,
// or null.
function vpParseTime(s) {
  s = String(s == null ? '' : s).trim();
  if (/^\d+(\.\d+)?$/.test(s)) return parseFloat(s);
  var m = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+(?:\.\d+)?)s)?$/.exec(s);
  if (m && s) return (+m[1] || 0) * 3600 + (+m[2] || 0) * 60 + (+m[3] || 0);
  if (/^\d+(:\d{1,2}){1,2}(\.\d+)?$/.test(s)) {
    return s.split(':').reduce(function (t, part) { return t * 60 + parseFloat(part); }, 0);
  }
  return null;
}

// The gallery's address for a video at time t: the fragment names the
// slide and the time, so a shared link opens the slideshow there. Only
// the fragment: the page's own path and query are left alone.
function vpHash(name, t) {
  return '#v=' + encodeURIComponent(name) + (t > 0 ? '&t=' + vpFmtTime(t) : '');
}

// What vpHash wrote: {v: name, t: seconds or null}, or null.
function vpParseHash(hash) {
  var h = String(hash || '').replace(/^#/, ''), out = { v: null, t: null };
  h.split('&').forEach(function (kv) {
    var i = kv.indexOf('='), k = i < 0 ? kv : kv.slice(0, i), v = i < 0 ? '' : kv.slice(i + 1);
    try { v = decodeURIComponent(v); } catch (e) { return; }
    if (k === 'v') out.v = v;
    else if (k === 't') out.t = vpParseTime(v);
  });
  return out.v ? out : null;
}

// Each marker group's color: its own when it has one, else the next of
// VP_COLORS.
function vpGroupColors(meta) {
  var colors = Object.create(null), n = 0, groups = (meta && meta.groups) || {};
  Object.keys(groups).sort().forEach(function (g) {
    colors[g] = groups[g] && groups[g].color ? groups[g].color : VP_COLORS[n++ % VP_COLORS.length];
  });
  ((meta && meta.markers) || []).forEach(function (mk) {
    if (mk.group && !colors[mk.group]) colors[mk.group] = VP_COLORS[n++ % VP_COLORS.length];
  });
  return colors;
}

// The timeline for meta on a video lasting dur seconds, in percent of its
// width: segments, the chapters, with untitled ones filling what they
// leave out, so the bar is whole (one segment for a video without);
// ticks, the markers at a moment; bands, the markers over a stretch.
// Markers of a kind in hidden aren't shown.
function vpLayout(meta, dur, hidden) {
  var out = { segments: [], ticks: [], bands: [] };
  if (!(dur > 0)) return out;
  hidden = hidden || Object.create(null);
  var pct = function (t) { return Math.max(0, Math.min(100, t / dur * 100)); };
  var seg = function (s, e, title, chapter) {
    out.segments.push({ start: s, end: e, title: title, chapter: chapter, l: pct(s), w: pct(e) - pct(s) });
  };
  var chapters = ((meta && meta.chapters) || []).filter(function (c) {
    return c.end > c.start && c.start < dur;
  }).slice().sort(function (a, b) { return a.start - b.start; });
  var at = 0;
  chapters.forEach(function (c) {
    var s = Math.max(c.start, at), e = Math.min(c.end, dur);
    if (s > at + 0.01) seg(at, s, '', false);
    if (e > s) seg(s, e, c.title || '', true);
    at = Math.max(at, e);
  });
  if (at < dur - 0.01 || !out.segments.length) seg(at, dur, '', false);
  var colors = vpGroupColors(meta);
  ((meta && meta.markers) || []).forEach(function (mk, i) {
    if (!(mk.time >= 0) || mk.time > dur || hidden[mk.kind || '']) return;
    var o = { i: i, time: mk.time, label: mk.label || mk.kind || '', kind: mk.kind || '', color: colors[mk.group] || '', l: pct(mk.time) };
    if (mk.end > mk.time) {
      o.end = Math.min(mk.end, dur);
      o.w = pct(o.end) - o.l;
      out.bands.push(o);
    } else {
      out.ticks.push(o);
    }
  });
  return out;
}

// What's at time t on layout: the chapter it's in (or null) and the
// nearest marker within tol seconds, or the stretch it's in.
function vpHover(layout, t, tol) {
  var out = { chapter: null, marker: null };
  layout.segments.forEach(function (s) {
    if (s.chapter && t >= s.start && t < s.end) out.chapter = s;
  });
  var best = Infinity;
  layout.ticks.forEach(function (m) {
    var d = Math.abs(m.time - t);
    if (d <= tol && d < best) { best = d; out.marker = m; }
  });
  if (!out.marker) {
    layout.bands.forEach(function (b) {
      if (t >= b.time && t <= b.end && !out.marker) out.marker = b;
    });
  }
  return out;
}

// The preview frame for time t: {url, x, y, w, h} on a sprite (thumbs, as
// the metadata gives it, with its URL resolved) or from a thumbnails
// track's cues (vpParseThumbsVTT), or null.
function vpThumbAt(thumbs, cues, t) {
  if (cues && cues.length) {
    var lo = 0, hi = cues.length - 1;
    while (lo < hi) {
      var mid = (lo + hi + 1) >> 1;
      if (cues[mid].start <= t) lo = mid; else hi = mid - 1;
    }
    var c = cues[lo];
    return t >= c.start - 0.001 ? c : null;
  }
  if (!thumbs || !thumbs.sprite || !(thumbs.interval > 0) || !thumbs.columns) return null;
  var i = Math.max(0, Math.min(thumbs.count - 1, Math.floor(t / thumbs.interval)));
  return {
    url: thumbs.spriteURL || thumbs.sprite, x: (i % thumbs.columns) * thumbs.width, y: Math.floor(i / thumbs.columns) * thumbs.height,
    w: thumbs.width, h: thumbs.height, sheetW: thumbs.columns * thumbs.width, sheetH: Math.ceil(thumbs.count / thumbs.columns) * thumbs.height
  };
}

function vpCueTime(s) {
  var parts = s.replace(',', '.').split(':');
  return parts.reduce(function (t, p) { return t * 60 + parseFloat(p); }, 0);
}

// The cues of a WebVTT thumbnails track: each an image, and with
// #xywh=x,y,w,h the part of it to show. resolve turns the image's URL,
// relative to the track, into one to load.
function vpParseThumbsVTT(text, resolve) {
  var cues = [];
  String(text).replace(/\r\n?/g, '\n').split(/\n\n+/).forEach(function (block) {
    var lines = block.split('\n');
    for (var i = 0; i < lines.length; i++) {
      var m = /^\s*((?:\d+:)?\d{1,2}:\d{2}[.,]\d{1,3})\s+-->\s+((?:\d+:)?\d{1,2}:\d{2}[.,]\d{1,3})/.exec(lines[i]);
      if (!m || !lines[i + 1]) continue;
      var ref = lines[i + 1].trim(), hash = ref.indexOf('#'), url = hash < 0 ? ref : ref.slice(0, hash);
      var xywh = /#xywh=(\d+),(\d+),(\d+),(\d+)/.exec(ref);
      cues.push({
        start: vpCueTime(m[1]), end: vpCueTime(m[2]), url: resolve ? resolve(url) : url,
        x: xywh ? +xywh[1] : 0, y: xywh ? +xywh[2] : 0, w: xywh ? +xywh[3] : 0, h: xywh ? +xywh[4] : 0
      });
      break;
    }
  });
  return cues.sort(function (a, b) { return a.start - b.start; });
}

// The next of times (sorted) after t, or (dir < 0) the one before it:
// within a second after one, back goes to the one before, as a
// player's previous button does.
function vpStop(times, t, dir) {
  var i;
  if (dir > 0) {
    for (i = 0; i < times.length; i++) if (times[i] > t + 0.25) return times[i];
    return null;
  }
  for (i = times.length - 1; i >= 0; i--) if (times[i] < t - 1) return times[i];
  return null;
}

// What a key does to the current video: an action for VideoDock.act, or
// null when it does nothing to it. Shift+[ and Shift+] step between
// chapters whatever the layout types for them.
function vpKey(e) {
  if (e.ctrlKey || e.metaKey || e.altKey) return null;
  if (e.shiftKey && e.code === 'BracketLeft') return 'chapter:-1';
  if (e.shiftKey && e.code === 'BracketRight') return 'chapter:1';
  switch (e.key) {
    case ' ': case 'k': case 'K': return 'toggle';
    case 'j': case 'J': return 'seek:-10';
    case 'l': case 'L': return 'seek:10';
    case 'ArrowLeft': return 'seek:-5';
    case 'ArrowRight': return 'seek:5';
    case ',': return 'frame:-1';
    case '.': return 'frame:1';
    case '[': return 'marker:-1';
    case ']': return 'marker:1';
    case '{': return 'chapter:-1';
    case '}': return 'chapter:1';
    case '<': return 'rate:-1';
    case '>': return 'rate:1';
    case 'c': case 'C': return 'captions';
    case 'm': case 'M': return 'mute';
    case 'f': case 'F': return 'fullscreen';
    case 'PageUp': return 'slide:-1';
    case 'PageDown': return 'slide:1';
  }
  return null;
}

// The timeline's own keys, as a slider: the arrows step 5 seconds, Page
// Up and Down a minute, Home and End go to either end. The time to go
// to, or null.
function vpSliderKey(e, t, dur) {
  switch (e.key) {
    case 'ArrowLeft': case 'ArrowDown': return Math.max(0, t - 5);
    case 'ArrowRight': case 'ArrowUp': return Math.min(dur, t + 5);
    case 'PageDown': return Math.max(0, t - 60);
    case 'PageUp': return Math.min(dur, t + 60);
    case 'Home': return 0;
    case 'End': return dur;
  }
  return null;
}

// The playing rate one step slower (d < 0) or faster than rate.
function vpRateStep(rate, d) {
  var i = VP_RATES.indexOf(rate);
  if (i < 0) i = VP_RATES.indexOf(1);
  return VP_RATES[Math.max(0, Math.min(VP_RATES.length - 1, i + d))];
}

// The background of a timeline segment from start to end: what's played
// (up to t) in the accent, what's buffered (ranges, [start, end] pairs) a
// lighter gray, the rest the track's. With the pointer on the timeline at
// hover, the stretch from t to there is lit a little, over all of that.
function vpSegmentFill(seg, t, ranges, hover) {
  var len = seg.end - seg.start;
  if (!(len > 0)) return '';
  var p = function (x) { return (Math.max(0, Math.min(1, (x - seg.start) / len)) * 100).toFixed(2) + '%'; };
  var stops = ['var(--vd-played) 0', 'var(--vd-played) ' + p(t)];
  var at = p(t);
  ranges.forEach(function (r) {
    if (r[1] <= Math.max(seg.start, t) || r[0] >= seg.end) return;
    var a = p(Math.max(r[0], t)), b = p(r[1]);
    stops.push('var(--vd-track) ' + at, 'var(--vd-track) ' + a, 'var(--vd-buffered) ' + a, 'var(--vd-buffered) ' + b);
    at = b;
  });
  stops.push('var(--vd-track) ' + at, 'var(--vd-track) 100%');
  var base = 'linear-gradient(to right,' + stops.join(',') + ')';
  if (hover == null || hover <= t || hover <= seg.start || t >= seg.end) return base;
  return 'linear-gradient(to right,transparent ' + p(t) + ',var(--vd-hover) ' + p(t) + ',var(--vd-hover) ' + p(hover) +
    ',transparent ' + p(hover) + '),' + base;
}

// The language's name in the viewer's language, where the browser knows
// it, for a subtitle the server could only name by its tag.
function vpLangName(lang, label) {
  if (!lang || label !== lang) return label;
  try {
    var dn = new Intl.DisplayNames([navigator.language || 'en'], { type: 'language' });
    return dn.of(lang) || label;
  } catch (e) {
    return label;
  }
}

// The dock's icons are the page's own symbols (vd-* in custom-index.html),
// drawn like the rest of its icons: a 24 grid, a 1.8 stroke, round ends.
function vpIcon(id) {
  return '<svg viewBox="0 0 24 24" aria-hidden="true"><use href="#vd-' + id + '"/></svg>';
}

// How long the pointer rests on a button before its tooltip shows.
var VP_TIP_DELAY = 400;

// VideoDock makes the dock. opts are what it asks of the slideshow:
// fullscreen() toggles full screen, isFullscreen() says whether it's on,
// slide(d) moves d slides, time(it, t, final) is told where the video is
// (on a pause, a seek, every few seconds while it plays, and finally when
// it's left), and activity() that the viewer is using the dock.
function VideoDock(opts) {
  var el = doc.createElement('div');
  el.className = 'vdock';
  el.hidden = true;
  var btn = function (cls, label, icon, key, more) {
    return '<button class="vd-btn ' + cls + '" type="button" aria-label="' + label + '"' +
      (key ? ' data-key="' + key + '"' : '') + (more || '') + '>' + icon + '</button>';
  };
  el.innerHTML =
    '<div class="vd-flash" aria-hidden="true"></div>' +
    '<div class="vd-cap" aria-live="off" hidden></div>' +
    '<div class="vd-panel">' +
    '<div class="vd-tl" role="slider" tabindex="0" aria-label="Seek" aria-valuemin="0" aria-valuemax="0" aria-valuenow="0">' +
    '<div class="vd-rail"><div class="vd-bands"></div><div class="vd-segs"></div><div class="vd-ticks"></div></div>' +
    '<div class="vd-head"></div>' +
    '<div class="vd-pop" aria-hidden="true" hidden>' +
    '<div class="vd-pop-frame" hidden><div class="vd-pop-img"></div><canvas class="vd-pop-canvas" width="0" height="0"></canvas></div>' +
    '<div class="vd-pop-text"><span class="vd-pop-label" hidden></span><span class="vd-pop-time"></span></div>' +
    '</div>' +
    '</div>' +
    '<div class="vd-row"><span class="vd-cluster vd-left">' +
    btn('vd-play', 'Play', vpIcon('play'), 'K') +
    '<span class="vd-time"><span class="vd-cur">0:00</span><span class="vd-sep">/</span><span class="vd-dur">0:00</span></span>' +
    '<span class="vd-chap"></span>' +
    '</span><span class="vd-gap"></span><span class="vd-cluster vd-right">' +
    '<span class="vd-group vd-mk" hidden>' +
    btn('vd-prev-mk', 'Previous marker', vpIcon('prev-mk'), '[') +
    btn('vd-mk-menu', 'Markers', vpIcon('markers'), '', ' aria-haspopup="menu" aria-expanded="false"') +
    btn('vd-next-mk', 'Next marker', vpIcon('next-mk'), ']') +
    '</span>' +
    '<span class="vd-group vd-ch" hidden>' +
    btn('vd-prev-ch', 'Previous chapter', vpIcon('prev-ch'), 'Shift [') +
    btn('vd-next-ch', 'Next chapter', vpIcon('next-ch'), 'Shift ]') +
    '</span>' +
    '<span class="vd-group vd-set">' +
    '<span class="vd-vol">' + btn('vd-mute', 'Mute', vpIcon('vol'), 'M') +
    '<span class="vd-vol-slide"><input class="vd-volume" type="range" min="0" max="1" step="0.05" value="1" aria-label="Volume"></span></span>' +
    btn('vd-cc', 'Subtitles', vpIcon('cc'), 'C', ' aria-haspopup="menu" aria-expanded="false" aria-pressed="false" hidden') +
    btn('vd-rate', 'Speed', '<span class="vd-rate-text">1×</span>', '&lt; &gt;', ' aria-haspopup="menu" aria-expanded="false"') +
    btn('vd-more', 'More', vpIcon('more'), '', ' aria-haspopup="menu" aria-expanded="false"') +
    '</span>' +
    btn('vd-full', 'Full screen', vpIcon('full'), 'F') + '</span>' +
    '</div>' +
    '</div>' +
    '<div class="vd-menu" role="menu" hidden><div class="vd-menu-title"></div><div class="vd-menu-list"></div></div>' +
    '<div class="vd-tip" aria-hidden="true" hidden><span class="vd-tip-label"></span><kbd class="vd-tip-key"></kbd></div>';
  var q = function (c) { return el.querySelector(c); };
  var tl = q('.vd-tl'), segsEl = q('.vd-segs'), ticksEl = q('.vd-ticks'), bandsEl = q('.vd-bands'), head = q('.vd-head');
  var pop = q('.vd-pop'), popFrame = q('.vd-pop-frame'), popImg = q('.vd-pop-img'), popCanvas = q('.vd-pop-canvas');
  var popLabel = q('.vd-pop-label'), popTime = q('.vd-pop-time');
  var btnPlay = q('.vd-play'), cur = q('.vd-cur'), durEl = q('.vd-dur'), chapEl = q('.vd-chap');
  var btnMute = q('.vd-mute'), volume = q('.vd-volume'), btnRate = q('.vd-rate'), rateText = q('.vd-rate-text');
  var btnCC = q('.vd-cc'), btnFull = q('.vd-full'), btnMore = q('.vd-more'), btnMarkers = q('.vd-mk-menu');
  var menu = q('.vd-menu'), menuTitle = q('.vd-menu-title'), menuList = q('.vd-menu-list');
  var cap = q('.vd-cap'), flash = q('.vd-flash'), tip = q('.vd-tip');

  var v = null, it = null, meta = null, layout = vpLayout(null, 0), hiddenKinds = Object.create(null), thumbCues = null;
  var segEls = [], dragging = false, wasPaused = true, pendingAt = null, lastReport = 0, metaFor = null;
  var tracks = [], capOn = -1, menuKind = '', hoverT = null, hoverX = 0;
  var metaCache = new Map();
  var CAPTIONS_KEY = 'gows-captions', VOLUME_KEY = 'gows-volume';

  function duration() {
    if (v && isFinite(v.duration) && v.duration > 0) return v.duration;
    return meta && meta.duration > 0 ? meta.duration : 0;
  }

  // A URL from the metadata, which is relative to the video's own.
  function resolve(rel, base) {
    try { return new URL(rel, new URL(base || it.url, location.href)).href; } catch (e) { return rel; }
  }

  // ---- Attaching to a video ----
  var EVENTS = ['timeupdate', 'progress', 'durationchange', 'loadedmetadata', 'play', 'pause', 'ratechange', 'volumechange', 'seeked', 'ended'];
  function onMedia(e) {
    switch (e.type) {
      case 'loadedmetadata':
      case 'durationchange':
        if (pendingAt != null && v.readyState >= 1) seekTo(pendingAt);
        render();
        refreshPop();
        break;
      case 'play': case 'pause':
        syncPlay();
        if (e.type === 'pause') report(false);
        // Playing again starts the wait before the controls fade.
        else if (opts.activity) opts.activity();
        break;
      case 'seeked':
        report(false);
        break;
      case 'ratechange':
        rateText.textContent = v.playbackRate + '×';
        break;
      case 'volumechange':
        syncVolume();
        store(VOLUME_KEY, v.muted ? '0' : String(v.volume));
        break;
      case 'timeupdate':
        if (!v.paused && performance.now() - lastReport > 5000) report(false);
        break;
    }
    update();
  }

  function report(final) {
    lastReport = performance.now();
    if (v && it && opts.time) opts.time(it, v.currentTime, final);
  }

  // Takes over the video v, item it, in the slide box. at is where to
  // start, in seconds, or null.
  function attach(video, item, box, at) {
    if (v === video) {
      if (at != null) seekTo(at);
      return;
    }
    detach();
    v = video;
    it = item;
    v.controls = false;
    box.appendChild(el);
    el.hidden = false;
    pendingAt = at;
    EVENTS.forEach(function (t) { v.addEventListener(t, onMedia); });
    v.addEventListener('click', onVideoClick);
    v.addEventListener('pointerdown', onVideoPointer);
    var vol = parseFloat(store(VOLUME_KEY));
    if (vol >= 0 && vol <= 1) { v.volume = vol || 1; v.muted = vol === 0; }
    if (pendingAt != null && v.readyState >= 1) seekTo(pendingAt);
    syncPlay();
    rateText.textContent = v.playbackRate + '×';
    syncFull();
    syncVolume();
    setMeta(metaCache.get(item.url) || null);
    loadMeta(item);
  }

  function detach() {
    closeMenu(false, true);
    hidePop();
    hideTip();
    preview.reset();
    if (!v) return null;
    report(true);
    var t = v.currentTime;
    EVENTS.forEach(function (ty) { v.removeEventListener(ty, onMedia); });
    v.removeEventListener('click', onVideoClick);
    v.removeEventListener('pointerdown', onVideoPointer);
    tracks.forEach(function (tr) { if (tr.track) tr.track.removeEventListener('cuechange', showCues); });
    v = null;
    it = null;
    el.hidden = true;
    cap.hidden = true;
    el.remove();
    return t;
  }

  // The dock covers the video, whose box in its slide is r. A narrow
  // video, or one on a touch screen, gets the compact row: what doesn't
  // fit goes in the More menu.
  function place(r) {
    el.style.left = r.x + 'px';
    el.style.top = r.y + 'px';
    el.style.width = r.w + 'px';
    el.style.height = r.h + 'px';
    el.classList.toggle('compact', r.w < 600 || !finePointer.matches);
    el.classList.toggle('tiny', r.w < 380);
  }

  // ---- The metadata ----
  function loadMeta(item) {
    if (metaCache.has(item.url) || !window.fetch || metaFor === item.url) return;
    metaFor = item.url;
    fetch(item.url + '?meta', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .catch(function () { return null; })
      .then(function (m) {
        if (metaFor === item.url) metaFor = null;
        if (!m) return;
        metaCache.set(item.url, m);
        if (it === item) setMeta(m);
      });
  }

  function setMeta(m) {
    meta = m;
    thumbCues = null;
    capOn = -1;
    tracks = [];
    hiddenKinds = Object.create(null);
    preview.reset();
    if (m && m.thumbnails && m.thumbnails.sprite) m.thumbnails.spriteURL = resolve(m.thumbnails.sprite);
    if (m && m.thumbnails && m.thumbnails.vtt && window.fetch) {
      var vttURL = resolve(m.thumbnails.vtt), forIt = it;
      fetch(vttURL, { credentials: 'same-origin' }).then(function (r) { return r.ok ? r.text() : ''; }).catch(function () { return ''; })
        .then(function (text) {
          if (it !== forIt || meta !== m) return;
          thumbCues = vpParseThumbsVTT(text, function (u) { return resolve(u, vttURL); });
          refreshPop();
        });
    }
    addTracks();
    el.classList.toggle('has-mk', !!(m && m.markers && m.markers.length));
    el.classList.toggle('has-ch', !!(m && m.chapters && m.chapters.length));
    q('.vd-mk').hidden = !(m && m.markers && m.markers.length);
    q('.vd-ch').hidden = !(m && m.chapters && m.chapters.length);
    render();
    refreshPop();
  }

  // ---- Subtitles ----
  // The tracks are the video's own <track>s, added once a video's
  // metadata is in, but the browser doesn't draw them: their cues are
  // drawn above the dock (showCues), where the controls can't cover them.
  function addTracks() {
    var subs = (meta && meta.subtitles) || [];
    btnCC.hidden = !subs.length;
    el.classList.toggle('has-cc', !!subs.length);
    if (!v || !subs.length) { syncCC(); return; }
    if (!v._vpTracks) {
      v._vpTracks = subs.map(function (s) {
        var tr = doc.createElement('track');
        tr.kind = 'subtitles';
        tr.label = vpLangName(s.lang, s.label);
        if (s.lang) tr.srclang = s.lang;
        tr.src = resolve(s.src);
        v.appendChild(tr);
        return tr;
      });
    }
    tracks = v._vpTracks;
    tracks.forEach(function (tr) { if (tr.track) tr.track.mode = 'disabled'; });
    // The viewer's last choice: a language, or off.
    var want = store(CAPTIONS_KEY), pick = -1;
    if (want && want !== 'off') {
      subs.forEach(function (s, i) { if (pick < 0 && (s.lang || s.label) === want) pick = i; });
    }
    setCaptions(pick, false);
  }

  function setCaptions(i, remember) {
    tracks.forEach(function (tr, k) {
      if (!tr.track) return;
      tr.track.removeEventListener('cuechange', showCues);
      tr.track.mode = k === i ? 'hidden' : 'disabled';
      if (k === i) tr.track.addEventListener('cuechange', showCues);
    });
    capOn = i;
    if (remember) {
      var s = meta && meta.subtitles && meta.subtitles[i];
      store(CAPTIONS_KEY, s ? (s.lang || s.label) : 'off');
    }
    showCues();
    syncCC();
  }

  function syncCC() {
    btnCC.setAttribute('aria-pressed', String(capOn >= 0));
  }

  function showCues() {
    var tr = tracks[capOn];
    var cues = tr && tr.track && tr.track.activeCues;
    cap.textContent = '';
    if (!cues || !cues.length) { cap.hidden = true; return; }
    for (var i = 0; i < cues.length; i++) {
      var line = doc.createElement('span');
      if (cues[i].getCueAsHTML) line.appendChild(cues[i].getCueAsHTML()); else line.textContent = cues[i].text;
      cap.appendChild(line);
    }
    cap.hidden = false;
  }

  // ---- Drawing the timeline ----
  function render() {
    var dur = duration();
    layout = vpLayout(meta, dur, hiddenKinds);
    segsEl.textContent = '';
    segEls = layout.segments.map(function (s) {
      var d = doc.createElement('div');
      d.className = 'vd-seg';
      d.style.left = s.l + '%';
      d.style.width = s.w + '%';
      segsEl.appendChild(d);
      return d;
    });
    segsEl.classList.toggle('chaptered', layout.segments.length > 1);
    ticksEl.textContent = '';
    layout.ticks.forEach(function (m) {
      var d = doc.createElement('div');
      d.className = 'vd-tick';
      d.style.left = m.l + '%';
      if (m.color) d.style.setProperty('--c', m.color);
      ticksEl.appendChild(d);
    });
    bandsEl.textContent = '';
    layout.bands.forEach(function (b) {
      var d = doc.createElement('div');
      d.className = 'vd-band';
      d.style.left = b.l + '%';
      d.style.width = Math.max(b.w, 0.4) + '%';
      if (b.color) d.style.setProperty('--c', b.color);
      bandsEl.appendChild(d);
    });
    durEl.textContent = vpFmtTime(dur);
    tl.setAttribute('aria-valuemax', String(Math.round(dur)));
    update();
  }

  function buffered() {
    var out = [];
    try {
      for (var i = 0; v && i < v.buffered.length; i++) out.push([v.buffered.start(i), v.buffered.end(i)]);
    } catch (e) { }
    return out;
  }

  var shownChapter = null;
  function update(t) {
    if (!v) return;
    var dur = duration();
    if (t == null) t = v.currentTime || 0;
    var ranges = buffered();
    layout.segments.forEach(function (s, i) {
      if (segEls[i]) segEls[i].style.background = vpSegmentFill(s, t, ranges, hoverT);
    });
    head.style.left = (dur > 0 ? Math.min(100, t / dur * 100) : 0) + '%';
    var long = dur >= 3600;
    cur.textContent = vpFmtTime(t, long);
    var h = vpHover(layout, t, 0);
    var title = h.chapter ? h.chapter.title : '';
    if (title !== shownChapter) { chapEl.textContent = title; shownChapter = title; }
    tl.setAttribute('aria-valuenow', String(Math.round(t)));
    tl.setAttribute('aria-valuetext', vpFmtTime(t, long) + ' of ' + vpFmtTime(dur, long) + (title ? ', ' + title : ''));
  }

  // ---- Seeking ----
  function seekTo(t) {
    if (!v) return;
    if (v.readyState < 1) { pendingAt = t; return; }
    pendingAt = null;
    var dur = duration();
    v.currentTime = Math.max(0, dur > 0 ? Math.min(dur - 0.05, t) : t);
    update(v.currentTime);
  }

  function showFlash(text) {
    flash.textContent = text;
    flash.classList.remove('on');
    void flash.offsetWidth;
    flash.classList.add('on');
  }

  function stopTimes(kind) {
    if (kind === 'chapter') return layout.segments.filter(function (s) { return s.chapter; }).map(function (s) { return s.start; });
    return layout.ticks.concat(layout.bands).map(function (m) { return m.time; }).sort(function (a, b) { return a - b; });
  }

  function jump(kind, d) {
    var to = vpStop(stopTimes(kind), v.currentTime, d);
    if (to == null && d < 0 && kind === 'chapter') to = 0;
    if (to == null) return;
    seekTo(to);
    var h = vpHover(layout, to, 0.01);
    var name = kind === 'chapter' ? (h.chapter && h.chapter.title) : (h.marker && h.marker.label);
    showFlash(name || vpFmtTime(to));
  }

  // Does action (see vpKey) to the video.
  function act(a) {
    if (!v) return false;
    var parts = a.split(':'), n = +parts[1];
    switch (parts[0]) {
      case 'toggle': if (v.paused) v.play().catch(function () { }); else v.pause(); break;
      case 'seek': seekTo(v.currentTime + n); showFlash((n > 0 ? '+' : '−') + Math.abs(n) + ' s'); break;
      case 'frame':
        if (!v.paused) v.pause();
        seekTo(v.currentTime + n / (meta && meta.fps > 0 ? meta.fps : 30));
        break;
      case 'marker': jump('marker', n); break;
      case 'chapter': jump('chapter', n); break;
      case 'rate': v.playbackRate = vpRateStep(v.playbackRate, n); showFlash(v.playbackRate + '×'); break;
      case 'captions':
        if (!tracks.length) return false;
        setCaptions(capOn >= 0 ? -1 : 0, true);
        showFlash(capOn >= 0 ? 'Subtitles: ' + tracks[capOn].label : 'Subtitles off');
        break;
      case 'mute': v.muted = !v.muted; break;
      case 'fullscreen': opts.fullscreen(); break;
      case 'slide': opts.slide(n); break;
      default: return false;
    }
    return true;
  }

  // A key pressed in the slideshow while this video is on: true when the
  // dock did something with it.
  function key(e) {
    if (!v) return false;
    var tag = e.target && e.target.tagName;
    if (menuOpen() && e.key === 'Escape') { closeMenu(true); return true; }
    if (tag === 'INPUT' && /^Arrow|^Page|^Home|^End/.test(e.key)) return false;
    if ((tag === 'BUTTON' || tag === 'A') && (e.key === ' ' || e.key === 'Enter')) return false;
    var a = vpKey(e);
    if (!a) return false;
    if (a === 'toggle' && e.key === ' ' && e.repeat) return true;
    return act(a);
  }

  // Whether the controls should stay up: while the video is paused, a
  // menu is open or the timeline is being dragged.
  function holdsChrome() {
    return !!v && (v.paused || menuOpen() || dragging);
  }

  // ---- The preview over the timeline ----
  // What the box shows for a time, best first: the server's sprite (or
  // the thumbnails track's images) once loaded; meanwhile, or without
  // one, a frame of the video itself, from a second, muted copy of it
  // that seeks to the time under the pointer (one seek at a time, the
  // latest wanted next); and with neither, no picture at all, just the
  // time and the chapter, never an empty box.
  var preview = (function () {
    var sheets = Object.create(null); // image URL -> {img, ready, failed}
    var sv = null, busy = false, want = null, drawn = false, shownT = -1, svFailed = false;

    function sheet(url) {
      var s = sheets[url];
      if (s) return s;
      s = sheets[url] = { ready: false, failed: false };
      var img = new Image();
      img.decoding = 'async';
      img.onload = function () { s.ready = true; refreshPop(); };
      img.onerror = function () { s.failed = true; refreshPop(); };
      img.src = url;
      s.img = img;
      return s;
    }

    // The sprite's or the track's frame at t, if its image has loaded.
    // Asking starts the image loading, the first time.
    function image(t) {
      var th = vpThumbAt(meta && meta.thumbnails, thumbCues, t);
      if (!th || !th.w || !th.h || !th.url) return null;
      var s = sheet(th.url);
      return s.ready ? th : null;
    }

    // Whether there's an image source at all that may still arrive.
    function imageComing(t) {
      var th = vpThumbAt(meta && meta.thumbnails, thumbCues, t);
      if (!th || !th.url) return false;
      var s = sheets[th.url];
      return !s || (!s.ready && !s.failed);
    }

    function size() {
      var w = 176, vw = (sv && sv.videoWidth) || (v && v.videoWidth) || 16, vh = (sv && sv.videoHeight) || (v && v.videoHeight) || 9;
      return { w: w, h: Math.max(40, Math.min(176, Math.round(w * vh / vw))) };
    }

    function frame(t) {
      if (svFailed || !v || !it) return;
      want = t;
      if (!sv) {
        sv = doc.createElement('video');
        sv.muted = true;
        sv.playsInline = true;
        sv.preload = 'auto';
        sv.setAttribute('aria-hidden', 'true');
        sv.addEventListener('loadedmetadata', next);
        sv.addEventListener('seeked', drawFrame);
        sv.addEventListener('error', function () { svFailed = true; busy = false; });
        sv.src = v.currentSrc || it.url;
      }
      next();
    }

    function next() {
      if (!sv || busy || want == null || sv.readyState < 1) return;
      if (Math.abs(want - shownT) < 0.2 && drawn) return;
      busy = true;
      var t = want;
      want = null;
      sv._t = t;
      // A seek to the keyframe is enough for a preview, and much faster.
      if (sv.fastSeek) sv.fastSeek(t); else sv.currentTime = t;
    }

    function drawFrame() {
      busy = false;
      if (!sv) return;
      var go = function () {
        if (!sv || !sv.videoWidth) { next(); return; }
        var s = size(), dpr = Math.min(window.devicePixelRatio || 1, 2);
        if (popCanvas.width !== Math.round(s.w * dpr)) { popCanvas.width = Math.round(s.w * dpr); popCanvas.height = Math.round(s.h * dpr); }
        try {
          popCanvas.getContext('2d').drawImage(sv, 0, 0, popCanvas.width, popCanvas.height);
          drawn = true;
          shownT = sv._t;
        } catch (e) {
          svFailed = true;
        }
        refreshPop(true);
        next();
      };
      if (sv.readyState >= 2) go(); else sv.addEventListener('loadeddata', go, { once: true });
    }

    function reset() {
      if (sv) {
        sv.removeAttribute('src');
        try { sv.load(); } catch (e) { }
      }
      sv = null;
      busy = false;
      want = null;
      drawn = false;
      shownT = -1;
      svFailed = false;
      sheets = Object.create(null);
    }

    return {
      image: image, frame: frame, reset: reset, size: size, imageComing: imageComing,
      hasFrame: function () { return drawn; },
      canFrame: function () { return !svFailed && !!popCanvas.getContext; }
    };
  })();

  function timeAt(x) {
    var r = tl.getBoundingClientRect(), dur = duration();
    return r.width > 0 && dur > 0 ? Math.max(0, Math.min(1, (x - r.left) / r.width)) * dur : 0;
  }

  // keepFrame: only the live frame changed, so the box stays as it is.
  function showPop(t, x, keepFrame) {
    var dur = duration();
    if (!(dur > 0)) return;
    hoverT = t;
    hoverX = x;
    var h = vpHover(layout, t, dur / 200);
    popTime.textContent = vpFmtTime(t, dur >= 3600);
    var label = h.marker ? h.marker.label : '';
    if (h.chapter && h.chapter.title) label = label ? h.chapter.title + ' · ' + label : h.chapter.title;
    popLabel.textContent = label;
    popLabel.hidden = !label;
    var th = preview.image(t);
    if (th) {
      var scale = Math.min(1, 176 / th.w);
      popFrame.hidden = false;
      popFrame.style.width = Math.round(th.w * scale) + 'px';
      popFrame.style.height = Math.round(th.h * scale) + 'px';
      popImg.hidden = false;
      popCanvas.hidden = true;
      popImg.style.backgroundImage = 'url("' + String(th.url).replace(/["\\]/g, '\\$&') + '")';
      popImg.style.backgroundPosition = (-th.x * scale) + 'px ' + (-th.y * scale) + 'px';
      popImg.style.backgroundSize = th.sheetW ? (th.sheetW * scale) + 'px ' + (th.sheetH * scale) + 'px' : '';
      el.dataset.preview = 'image';
    } else {
      // No image yet: the video's own frame, as soon as it's drawn.
      if (!keepFrame && preview.canFrame()) preview.frame(t);
      var hasFrame = preview.hasFrame();
      popFrame.hidden = !hasFrame;
      popImg.hidden = true;
      popCanvas.hidden = !hasFrame;
      if (hasFrame) {
        var s = preview.size();
        popFrame.style.width = s.w + 'px';
        popFrame.style.height = s.h + 'px';
      }
      el.dataset.preview = hasFrame ? 'frame' : 'none';
    }
    pop.hidden = false;
    var r = tl.getBoundingClientRect(), half = pop.offsetWidth / 2;
    pop.style.left = Math.max(half, Math.min(r.width - half, x - r.left)) + 'px';
    update();
  }

  // The box again for the time already under the pointer, when what it
  // can show changed: the metadata or an image arrived, a frame was drawn.
  function refreshPop(frameOnly) {
    if (!pop.hidden && hoverT != null) showPop(timeAt(hoverX), hoverX, frameOnly);
  }

  function hidePop() {
    pop.hidden = true;
    hoverT = null;
    if (v) update();
  }

  // ---- The pointer on the timeline ----
  tl.addEventListener('pointerdown', function (e) {
    if (!v || e.button > 0) return;
    e.preventDefault();
    try { tl.setPointerCapture(e.pointerId); } catch (err) { }
    dragging = true;
    wasPaused = v.paused;
    if (!v.paused) v.pause();
    el.classList.add('scrubbing');
    var t = timeAt(e.clientX);
    update(t);
    showPop(t, e.clientX);
    tl.focus({ preventScroll: true });
    if (opts.activity) opts.activity();
  });
  tl.addEventListener('pointermove', function (e) {
    if (!v) return;
    var t = timeAt(e.clientX);
    if (dragging) {
      if (v.fastSeek) v.fastSeek(t); else v.currentTime = t;
    }
    if (dragging || e.pointerType === 'mouse' || e.pointerType === 'pen') showPop(t, e.clientX);
    if (dragging) update(t);
  });
  function endDrag(e) {
    if (!dragging) return;
    dragging = false;
    el.classList.remove('scrubbing');
    if (e.type === 'pointerup') seekTo(timeAt(e.clientX));
    if (!wasPaused) v.play().catch(function () { });
    if (e.pointerType !== 'mouse') hidePop();
  }
  tl.addEventListener('pointerup', endDrag);
  tl.addEventListener('pointercancel', endDrag);
  tl.addEventListener('pointerleave', function () { if (!dragging) hidePop(); });
  tl.addEventListener('keydown', function (e) {
    if (!v) return;
    var t = vpSliderKey(e, v.currentTime, duration());
    if (t == null) return;
    e.preventDefault();
    e.stopPropagation();
    seekTo(t);
  });

  // ---- The buttons ----
  function onVideoPointer(e) { v._vpPointer = e.pointerType; }
  function onVideoClick() {
    // A mouse click plays or pauses; a tap shows the controls instead
    // (the slideshow's own tap).
    if (v && v._vpPointer === 'mouse') act('toggle');
  }

  btnPlay.addEventListener('click', function () { act('toggle'); });
  q('.vd-prev-mk').addEventListener('click', function () { act('marker:-1'); });
  q('.vd-next-mk').addEventListener('click', function () { act('marker:1'); });
  q('.vd-prev-ch').addEventListener('click', function () { act('chapter:-1'); });
  q('.vd-next-ch').addEventListener('click', function () { act('chapter:1'); });
  btnMute.addEventListener('click', function () { act('mute'); });
  // The slider's own keys change the volume, and nothing else: not the
  // slide, as the arrows and Home and End do in the slideshow.
  volume.addEventListener('keydown', function (e) {
    if (/^(Arrow|Page|Home$|End$)/.test(e.key)) e.stopPropagation();
  });
  volume.addEventListener('input', function () {
    if (!v) return;
    v.volume = +volume.value;
    v.muted = +volume.value === 0;
  });
  btnFull.addEventListener('click', function () { act('fullscreen'); });

  function syncPlay() {
    if (!v) return;
    el.classList.toggle('paused', v.paused);
    btnPlay.innerHTML = vpIcon(v.paused ? 'play' : 'pause');
    btnPlay.setAttribute('aria-label', v.paused ? 'Play' : 'Pause');
  }

  function syncVolume() {
    if (!v) return;
    var muted = v.muted || v.volume === 0;
    btnMute.innerHTML = vpIcon(muted ? 'mute' : v.volume < 0.5 ? 'vol-low' : 'vol');
    btnMute.setAttribute('aria-label', muted ? 'Unmute' : 'Mute');
    volume.value = String(muted ? 0 : v.volume);
    volume.style.setProperty('--vol', (muted ? 0 : v.volume * 100) + '%');
  }

  function syncFull() {
    var on = opts.isFullscreen();
    btnFull.innerHTML = vpIcon(on ? 'exit-full' : 'full');
    btnFull.setAttribute('aria-label', on ? 'Exit full screen' : 'Full screen');
  }

  // ---- Tooltips ----
  // One tooltip for every button: its name and its key, shown above it
  // after the pointer rests there, or at once for keyboard focus.
  var tipTimer = 0, tipFor = null;
  function showTip(b) {
    if (!b || b.disabled || menuOpen()) return;
    tipFor = b;
    q('.vd-tip-label').textContent = b.getAttribute('aria-label');
    var k = b.getAttribute('data-key');
    q('.vd-tip-key').textContent = k || '';
    q('.vd-tip-key').hidden = !k;
    tip.hidden = false;
    var br = b.getBoundingClientRect(), er = el.getBoundingClientRect(), w = tip.offsetWidth;
    var x = br.left - er.left + br.width / 2 - w / 2;
    tip.style.left = Math.max(6, Math.min(er.width - w - 6, x)) + 'px';
    tip.style.top = (br.top - er.top - tip.offsetHeight - 10) + 'px';
    tip.classList.add('on');
  }
  function hideTip() {
    clearTimeout(tipTimer);
    tipFor = null;
    tip.classList.remove('on');
    tip.hidden = true;
  }
  el.addEventListener('pointerover', function (e) {
    var b = e.target.closest && e.target.closest('.vd-btn');
    if (!b || e.pointerType === 'touch' || b === tipFor) return;
    hideTip();
    tipTimer = setTimeout(function () { showTip(b); }, VP_TIP_DELAY);
  });
  el.addEventListener('pointerout', function (e) {
    var b = e.target.closest && e.target.closest('.vd-btn');
    if (b && !b.contains(e.relatedTarget)) hideTip();
  });
  el.addEventListener('focusin', function (e) {
    var b = e.target.closest && e.target.closest('.vd-btn');
    if (b && b.matches && b.matches(':focus-visible')) showTip(b);
  });
  el.addEventListener('focusout', hideTip);
  el.addEventListener('pointerdown', hideTip);

  // ---- Menus: speed, subtitles, marker kinds, and More ----
  // A popover over its button: a title, then items with a check on the
  // current choice. Arrow keys move, Home and End jump, Escape or Tab
  // closes it, and it fades and rises into place.
  var menuAnchor = null, menuCloseTimer = 0;
  // Open, as opposed to shown while it fades out after closing: a key
  // pressed then is not the menu's.
  function menuOpen() {
    return menu.classList.contains('open');
  }
  function openMenu(kind, anchor, title, items) {
    if (menuKind === kind && menuOpen()) { closeMenu(true); return; }
    closeMenu(false, true);
    hideTip();
    clearTimeout(menuCloseTimer);
    menuKind = kind;
    menuTitle.textContent = title;
    menuList.textContent = '';
    menu.setAttribute('aria-label', title);
    items.forEach(function (o) {
      var b = doc.createElement('button');
      b.type = 'button';
      b.setAttribute('role', o.check ? 'menuitemcheckbox' : o.action ? 'menuitem' : 'menuitemradio');
      if (!o.action) b.setAttribute('aria-checked', String(!!o.on));
      b.innerHTML = vpIcon('check');
      if (o.color) {
        var dot = doc.createElement('span');
        dot.className = 'vd-dot';
        dot.style.background = o.color;
        b.appendChild(dot);
      }
      var text = doc.createElement('span');
      text.className = 'vd-menu-text';
      text.textContent = o.label;
      b.appendChild(text);
      if (o.hint) {
        var hint = doc.createElement('span');
        hint.className = 'vd-menu-hint';
        hint.textContent = o.hint;
        b.appendChild(hint);
      }
      b.addEventListener('click', function () {
        if (o.check) { o.pick(); b.setAttribute('aria-checked', String(!!o.state())); return; }
        // An item that opens another menu does so from the same button.
        closeMenu(!o.keepFocus, !!o.keepFocus);
        o.pick();
      });
      menuList.appendChild(b);
    });
    menu.hidden = false;
    el.classList.add('menu-open');
    var ar = anchor.getBoundingClientRect(), er = el.getBoundingClientRect();
    menu.style.right = Math.max(6, er.right - ar.right) + 'px';
    menu.style.bottom = Math.max(6, er.bottom - ar.top + 8) + 'px';
    void menu.offsetWidth;
    menu.classList.add('open');
    var first = menuList.querySelector('[aria-checked="true"]') || menuList.firstChild;
    if (first) first.focus();
    anchor.setAttribute('aria-expanded', 'true');
    menuAnchor = anchor;
  }

  // refocus puts the focus back on the menu's button; now skips the fade.
  function closeMenu(refocus, now) {
    if (!menuOpen()) {
      // Fading out already: now, it goes at once.
      if (now) menu.hidden = true;
      return;
    }
    menu.classList.remove('open');
    el.classList.remove('menu-open');
    menuKind = '';
    var a = menuAnchor;
    menuAnchor = null;
    if (a) {
      a.setAttribute('aria-expanded', 'false');
      if (refocus) a.focus();
    }
    clearTimeout(menuCloseTimer);
    if (now || reducedMotion.matches) menu.hidden = true;
    else menuCloseTimer = setTimeout(function () { if (!menu.classList.contains('open')) menu.hidden = true; }, 160);
    if (opts.activity) opts.activity();
  }

  menu.addEventListener('keydown', function (e) {
    var items = Array.prototype.slice.call(menuList.children), i = items.indexOf(doc.activeElement), n = null;
    switch (e.key) {
      case 'ArrowDown': n = items[(i + 1) % items.length]; break;
      case 'ArrowUp': n = items[(i - 1 + items.length) % items.length]; break;
      case 'Home': n = items[0]; break;
      case 'End': n = items[items.length - 1]; break;
      case 'Escape': e.preventDefault(); e.stopPropagation(); closeMenu(true); return;
      case 'Tab': closeMenu(true, true); e.preventDefault(); return;
      default: return;
    }
    e.preventDefault();
    e.stopPropagation();
    if (n) n.focus();
  });
  doc.addEventListener('pointerdown', function (e) {
    if (menuOpen() && !menu.contains(e.target) && !(menuAnchor && menuAnchor.contains(e.target))) closeMenu(false);
  }, true);

  function rateMenu(anchor) {
    openMenu('rate', anchor, 'Speed', VP_RATES.map(function (r) {
      return { label: r === 1 ? 'Normal' : r + '×', on: v.playbackRate === r, pick: function () { v.playbackRate = r; } };
    }));
  }

  function ccMenu(anchor) {
    var items = [{ label: 'Off', on: capOn < 0, pick: function () { setCaptions(-1, true); } }];
    tracks.forEach(function (tr, i) {
      items.push({ label: tr.label, on: capOn === i, pick: function () { setCaptions(i, true); } });
    });
    openMenu('cc', anchor, 'Subtitles', items);
  }

  function markersMenu(anchor) {
    var kinds = Object.create(null), order = [], colors = vpGroupColors(meta);
    (meta.markers || []).forEach(function (mk) {
      var k = mk.kind || '';
      if (!(k in kinds)) { kinds[k] = { n: 0, color: colors[mk.group] || '' }; order.push(k); }
      kinds[k].n++;
    });
    openMenu('markers', anchor, 'Show markers', order.map(function (k) {
      return {
        label: k || 'Other', hint: numFmt.format(kinds[k].n), color: kinds[k].color, check: true, on: !hiddenKinds[k],
        pick: function () {
          if (hiddenKinds[k]) delete hiddenKinds[k]; else hiddenKinds[k] = true;
          render();
        },
        state: function () { return !hiddenKinds[k]; }
      };
    }));
  }

  // What the compact row leaves out, in one menu.
  function moreMenu() {
    var items = [{
      label: 'Speed', hint: v.playbackRate === 1 ? 'Normal' : v.playbackRate + '×', action: true, keepFocus: true,
      pick: function () { rateMenu(btnMore); }
    }];
    if (tracks.length && el.classList.contains('tiny')) {
      items.push({ label: 'Subtitles', hint: capOn >= 0 ? tracks[capOn].label : 'Off', action: true, keepFocus: true, pick: function () { ccMenu(btnMore); } });
    }
    if (layout.segments.some(function (s) { return s.chapter; })) {
      items.push({ label: 'Previous chapter', action: true, pick: function () { act('chapter:-1'); } });
      items.push({ label: 'Next chapter', action: true, pick: function () { act('chapter:1'); } });
    }
    if (meta && meta.markers && meta.markers.length) {
      items.push({ label: 'Previous marker', action: true, pick: function () { act('marker:-1'); } });
      items.push({ label: 'Next marker', action: true, pick: function () { act('marker:1'); } });
      items.push({ label: 'Show markers', action: true, keepFocus: true, pick: function () { markersMenu(btnMore); } });
    }
    openMenu('more', btnMore, 'More', items);
  }

  btnRate.addEventListener('click', function () { if (v) rateMenu(btnRate); });
  btnCC.addEventListener('click', function () { if (v && meta) ccMenu(btnCC); });
  btnMarkers.addEventListener('click', function () { if (v && meta) markersMenu(btnMarkers); });
  btnMore.addEventListener('click', function () { if (v) moreMenu(); });

  // Using the dock keeps the controls up.
  el.addEventListener('pointermove', function () { if (opts.activity) opts.activity(); });

  return {
    el: el, attach: attach, detach: detach, place: place, act: act, key: key, syncFull: syncFull,
    holdsChrome: holdsChrome,
    current: function () { return v; },
    time: function () { return v ? v.currentTime : 0; },
    flash: showFlash
  };
}
