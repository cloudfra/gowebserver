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
  var colors = {}, n = 0, groups = (meta && meta.groups) || {};
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
  hidden = hidden || {};
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
// lighter gray, the rest the track's.
function vpSegmentFill(seg, t, ranges) {
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
  return 'linear-gradient(to right,' + stops.join(',') + ')';
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

var VP_ICONS = {
  play: '<svg viewBox="0 0 24 24"><path class="i-fill" d="M7 4.8v14.4a1 1 0 001.5.86l11.6-7.2a1 1 0 000-1.72L8.5 3.94A1 1 0 007 4.8z"/></svg>',
  pause: '<svg viewBox="0 0 24 24"><rect class="i-fill" x="6" y="4.5" width="4" height="15" rx="1"/><rect class="i-fill" x="14" y="4.5" width="4" height="15" rx="1"/></svg>',
  prevCh: '<svg viewBox="0 0 24 24"><path d="M6 5v14"/><path d="M18 6l-8 6 8 6z"/></svg>',
  nextCh: '<svg viewBox="0 0 24 24"><path d="M18 5v14"/><path d="M6 6l8 6-8 6z"/></svg>',
  prevMk: '<svg viewBox="0 0 24 24"><path d="M15 6l-6 6 6 6"/><path d="M6 4v16"/></svg>',
  nextMk: '<svg viewBox="0 0 24 24"><path d="M9 6l6 6-6 6"/><path d="M18 4v16"/></svg>',
  markers: '<svg viewBox="0 0 24 24"><path d="M6 21V4"/><path d="M6 4h11l-2.5 4L17 12H6"/></svg>',
  vol: '<svg viewBox="0 0 24 24"><path class="i-fill" d="M4 9h3l5-4v14l-5-4H4z"/><path d="M15.5 8.5a5 5 0 010 7"/><path d="M18 6a8.5 8.5 0 010 12"/></svg>',
  mute: '<svg viewBox="0 0 24 24"><path class="i-fill" d="M4 9h3l5-4v14l-5-4H4z"/><path d="M16 9l5 6M21 9l-5 6"/></svg>',
  cc: '<svg viewBox="0 0 24 24"><rect x="3" y="5" width="18" height="14" rx="3"/><path d="M10.5 10.2a2.2 2.2 0 100 3.6M17 10.2a2.2 2.2 0 100 3.6"/></svg>',
  full: '<svg viewBox="0 0 24 24"><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5"/></svg>',
  exitFull: '<svg viewBox="0 0 24 24"><path d="M9 4v5H4M15 4v5h5M9 20v-5H4M15 20v-5h5"/></svg>'
};

// VideoDock makes the dock. opts are what it asks of the slideshow:
// fullscreen() toggles full screen, isFullscreen() says whether it's on,
// slide(d) moves d slides, time(it, t, final) is told where the video is
// (on a pause, a seek, every few seconds while it plays, and finally when
// it's left), and activity() that the viewer is using the dock.
function VideoDock(opts) {
  var el = doc.createElement('div');
  el.className = 'vdock';
  el.hidden = true;
  el.innerHTML =
    '<div class="vd-flash" aria-hidden="true"></div>' +
    '<div class="vd-cap" aria-live="off" hidden></div>' +
    '<div class="vd-panel">' +
    '<div class="vd-tl" role="slider" tabindex="0" aria-label="Seek" aria-valuemin="0" aria-valuemax="0" aria-valuenow="0">' +
    '<div class="vd-bands"></div><div class="vd-segs"></div><div class="vd-ticks"></div><div class="vd-head"></div>' +
    '<div class="vd-pop" aria-hidden="true" hidden><div class="vd-pop-img"></div><div class="vd-pop-label"></div><div class="vd-pop-time"></div></div>' +
    '</div>' +
    '<div class="vd-row">' +
    '<button class="vd-btn vd-play" type="button" aria-label="Play" data-key="K">' + VP_ICONS.play + '</button>' +
    '<span class="vd-time"><span class="vd-cur">0:00</span><span class="vd-sep"> / </span><span class="vd-dur">0:00</span></span>' +
    '<span class="vd-chap"></span>' +
    '<span class="vd-gap"></span>' +
    '<span class="vd-group vd-mk" hidden>' +
    '<button class="vd-btn vd-prev-mk" type="button" aria-label="Previous marker" data-key="[">' + VP_ICONS.prevMk + '</button>' +
    '<button class="vd-btn vd-mk-menu" type="button" aria-label="Markers" aria-haspopup="menu">' + VP_ICONS.markers + '</button>' +
    '<button class="vd-btn vd-next-mk" type="button" aria-label="Next marker" data-key="]">' + VP_ICONS.nextMk + '</button>' +
    '</span>' +
    '<span class="vd-group vd-ch" hidden>' +
    '<button class="vd-btn vd-prev-ch" type="button" aria-label="Previous chapter" data-key="Shift+[">' + VP_ICONS.prevCh + '</button>' +
    '<button class="vd-btn vd-next-ch" type="button" aria-label="Next chapter" data-key="Shift+]">' + VP_ICONS.nextCh + '</button>' +
    '</span>' +
    '<span class="vd-vol">' +
    '<button class="vd-btn vd-mute" type="button" aria-label="Mute" data-key="M">' + VP_ICONS.vol + '</button>' +
    '<input class="vd-volume" type="range" min="0" max="1" step="0.05" value="1" aria-label="Volume">' +
    '</span>' +
    '<button class="vd-btn vd-rate" type="button" aria-label="Speed" aria-haspopup="menu" data-key="&lt; &gt;">1×</button>' +
    '<button class="vd-btn vd-cc" type="button" aria-label="Subtitles" aria-haspopup="menu" aria-pressed="false" data-key="C" hidden>' + VP_ICONS.cc + '</button>' +
    '<button class="vd-btn vd-full" type="button" aria-label="Full screen" data-key="F">' + VP_ICONS.full + '</button>' +
    '</div>' +
    '</div>' +
    '<div class="vd-menu" role="menu" hidden></div>';
  var q = function (c) { return el.querySelector(c); };
  var tl = q('.vd-tl'), segsEl = q('.vd-segs'), ticksEl = q('.vd-ticks'), bandsEl = q('.vd-bands'), head = q('.vd-head');
  var pop = q('.vd-pop'), popImg = q('.vd-pop-img'), popLabel = q('.vd-pop-label'), popTime = q('.vd-pop-time');
  var btnPlay = q('.vd-play'), cur = q('.vd-cur'), durEl = q('.vd-dur'), chapEl = q('.vd-chap');
  var btnMute = q('.vd-mute'), volume = q('.vd-volume'), btnRate = q('.vd-rate'), btnCC = q('.vd-cc'), btnFull = q('.vd-full');
  var menu = q('.vd-menu'), cap = q('.vd-cap'), flash = q('.vd-flash');

  var v = null, it = null, meta = null, layout = vpLayout(null, 0), hiddenKinds = {}, thumbCues = null;
  var segEls = [], dragging = false, wasPaused = true, pendingAt = null, lastReport = 0, metaFor = null;
  var tracks = [], capOn = -1, menuKind = '';
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
        break;
      case 'play': case 'pause':
        el.classList.toggle('paused', v.paused);
        btnPlay.innerHTML = v.paused ? VP_ICONS.play : VP_ICONS.pause;
        btnPlay.setAttribute('aria-label', v.paused ? 'Play' : 'Pause');
        if (e.type === 'pause') report(false);
        break;
      case 'seeked':
        report(false);
        break;
      case 'ratechange':
        btnRate.textContent = v.playbackRate + '×';
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
    el.classList.toggle('paused', v.paused);
    btnPlay.innerHTML = v.paused ? VP_ICONS.play : VP_ICONS.pause;
    btnPlay.setAttribute('aria-label', v.paused ? 'Play' : 'Pause');
    btnRate.textContent = v.playbackRate + '×';
    syncFull();
    syncVolume();
    setMeta(metaCache.get(item.url) || null);
    loadMeta(item);
  }

  function detach() {
    closeMenu();
    hidePop();
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

  // The dock covers the video, whose box in its slide is r.
  function place(r) {
    el.style.left = r.x + 'px';
    el.style.top = r.y + 'px';
    el.style.width = r.w + 'px';
    el.style.height = r.h + 'px';
    el.classList.toggle('narrow', r.w < 560);
    el.classList.toggle('tiny', r.w < 340);
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
    hiddenKinds = {};
    if (m && m.thumbnails && m.thumbnails.sprite) m.thumbnails.spriteURL = resolve(m.thumbnails.sprite);
    if (m && m.thumbnails && m.thumbnails.vtt && window.fetch) {
      var vttURL = resolve(m.thumbnails.vtt), forIt = it;
      fetch(vttURL, { credentials: 'same-origin' }).then(function (r) { return r.ok ? r.text() : ''; }).catch(function () { return ''; })
        .then(function (text) {
          if (it !== forIt || meta !== m) return;
          thumbCues = vpParseThumbsVTT(text, function (u) { return resolve(u, vttURL); });
        });
    }
    addTracks();
    q('.vd-mk').hidden = !(m && m.markers && m.markers.length);
    q('.vd-ch').hidden = !(m && m.chapters && m.chapters.length);
    render();
  }

  // ---- Subtitles ----
  // The tracks are the video's own <track>s, added once a video's
  // metadata is in, but the browser doesn't draw them: their cues are
  // drawn above the dock (showCues), where the controls can't cover them.
  function addTracks() {
    var subs = (meta && meta.subtitles) || [];
    btnCC.hidden = !subs.length;
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
      if (m.color) d.style.background = m.color;
      ticksEl.appendChild(d);
    });
    bandsEl.textContent = '';
    layout.bands.forEach(function (b) {
      var d = doc.createElement('div');
      d.className = 'vd-band';
      d.style.left = b.l + '%';
      d.style.width = Math.max(b.w, 0.4) + '%';
      if (b.color) d.style.background = b.color;
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
      if (segEls[i]) segEls[i].style.background = vpSegmentFill(s, t, ranges);
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
    if (!menu.hidden && e.key === 'Escape') { closeMenu(true); return true; }
    if (tag === 'INPUT' && /^Arrow|^Page|^Home|^End/.test(e.key)) return false;
    if ((tag === 'BUTTON' || tag === 'A') && (e.key === ' ' || e.key === 'Enter')) return false;
    var a = vpKey(e);
    if (!a) return false;
    if (a === 'toggle' && e.key === ' ' && e.repeat) return true;
    return act(a);
  }

  // ---- The pointer on the timeline ----
  function timeAt(x) {
    var r = tl.getBoundingClientRect(), dur = duration();
    return r.width > 0 && dur > 0 ? Math.max(0, Math.min(1, (x - r.left) / r.width)) * dur : 0;
  }

  function showPop(t, x) {
    var dur = duration();
    if (!(dur > 0)) return;
    var h = vpHover(layout, t, dur / 200);
    var th = vpThumbAt(meta && meta.thumbnails, thumbCues, t);
    popTime.textContent = vpFmtTime(t, dur >= 3600);
    var label = h.marker ? h.marker.label : '';
    if (h.chapter && h.chapter.title) label = label ? h.chapter.title + ' · ' + label : h.chapter.title;
    popLabel.textContent = label;
    popLabel.hidden = !label;
    if (th && th.w && th.h) {
      var scale = Math.min(1, 180 / th.w);
      popImg.hidden = false;
      popImg.style.width = Math.round(th.w * scale) + 'px';
      popImg.style.height = Math.round(th.h * scale) + 'px';
      popImg.style.backgroundImage = 'url("' + String(th.url).replace(/["\\]/g, '\\$&') + '")';
      popImg.style.backgroundPosition = (-th.x * scale) + 'px ' + (-th.y * scale) + 'px';
      popImg.style.backgroundSize = th.sheetW ? (th.sheetW * scale) + 'px ' + (th.sheetH * scale) + 'px' : '';
    } else {
      popImg.hidden = true;
    }
    pop.hidden = false;
    var r = tl.getBoundingClientRect(), half = pop.offsetWidth / 2;
    var left = Math.max(half, Math.min(r.width - half, x - r.left));
    pop.style.left = left + 'px';
  }

  function hidePop() {
    pop.hidden = true;
  }

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
      update(t);
      if (v.fastSeek) v.fastSeek(t); else v.currentTime = t;
    }
    if (dragging || e.pointerType === 'mouse') showPop(t, e.clientX);
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
  volume.addEventListener('input', function () {
    if (!v) return;
    v.volume = +volume.value;
    v.muted = +volume.value === 0;
  });
  btnFull.addEventListener('click', function () { act('fullscreen'); });

  function syncVolume() {
    if (!v) return;
    var muted = v.muted || v.volume === 0;
    btnMute.innerHTML = muted ? VP_ICONS.mute : VP_ICONS.vol;
    btnMute.setAttribute('aria-label', muted ? 'Unmute' : 'Mute');
    volume.value = String(muted ? 0 : v.volume);
  }

  function syncFull() {
    var on = opts.isFullscreen();
    btnFull.innerHTML = on ? VP_ICONS.exitFull : VP_ICONS.full;
    btnFull.setAttribute('aria-label', on ? 'Exit full screen' : 'Full screen');
  }

  // ---- Menus: speed, subtitles, marker kinds ----
  function openMenu(kind, anchor, items) {
    if (menuKind === kind && !menu.hidden) { closeMenu(true); return; }
    menuKind = kind;
    menu.textContent = '';
    menu.setAttribute('aria-label', anchor.getAttribute('aria-label'));
    items.forEach(function (o) {
      var b = doc.createElement('button');
      b.type = 'button';
      b.setAttribute('role', o.check ? 'menuitemcheckbox' : 'menuitemradio');
      b.setAttribute('aria-checked', String(!!o.on));
      if (o.color) {
        var dot = doc.createElement('span');
        dot.className = 'vd-dot';
        dot.style.background = o.color;
        b.appendChild(dot);
      }
      var text = doc.createElement('span');
      text.textContent = o.label;
      b.appendChild(text);
      b.addEventListener('click', function () {
        o.pick();
        if (o.check) { b.setAttribute('aria-checked', String(!!o.state())); return; }
        closeMenu(true);
      });
      menu.appendChild(b);
    });
    menu.hidden = false;
    el.classList.add('menu-open');
    var ar = anchor.getBoundingClientRect(), er = el.getBoundingClientRect();
    menu.style.right = Math.max(4, er.right - ar.right) + 'px';
    var first = menu.querySelector('[aria-checked="true"]') || menu.firstChild;
    if (first) first.focus();
    anchor.setAttribute('aria-expanded', 'true');
    menu._anchor = anchor;
  }

  function closeMenu(refocus) {
    if (menu.hidden) return;
    menu.hidden = true;
    el.classList.remove('menu-open');
    var a = menu._anchor;
    menuKind = '';
    if (a) {
      a.setAttribute('aria-expanded', 'false');
      if (refocus) a.focus();
    }
  }

  menu.addEventListener('keydown', function (e) {
    var items = Array.prototype.slice.call(menu.children), i = items.indexOf(doc.activeElement);
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      e.stopPropagation();
      var n = items[(i + (e.key === 'ArrowDown' ? 1 : items.length - 1)) % items.length];
      if (n) n.focus();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      closeMenu(true);
    }
  });
  doc.addEventListener('pointerdown', function (e) {
    if (!menu.hidden && !menu.contains(e.target) && !(menu._anchor && menu._anchor.contains(e.target))) closeMenu(false);
  }, true);

  btnRate.addEventListener('click', function () {
    if (!v) return;
    openMenu('rate', btnRate, VP_RATES.map(function (r) {
      return { label: r === 1 ? 'Normal' : r + '×', on: v.playbackRate === r, pick: function () { v.playbackRate = r; } };
    }));
  });

  btnCC.addEventListener('click', function () {
    if (!v || !meta) return;
    var items = [{ label: 'Off', on: capOn < 0, pick: function () { setCaptions(-1, true); } }];
    tracks.forEach(function (tr, i) {
      items.push({ label: tr.label, on: capOn === i, pick: function () { setCaptions(i, true); } });
    });
    openMenu('cc', btnCC, items);
  });

  q('.vd-mk-menu').addEventListener('click', function (e) {
    if (!v || !meta) return;
    var kinds = {}, order = [], colors = vpGroupColors(meta);
    (meta.markers || []).forEach(function (mk) {
      var k = mk.kind || '';
      if (!(k in kinds)) { kinds[k] = { n: 0, color: colors[mk.group] || '' }; order.push(k); }
      kinds[k].n++;
    });
    openMenu('markers', e.currentTarget, order.map(function (k) {
      return {
        label: (k || 'Other') + ' (' + numFmt.format(kinds[k].n) + ')', color: kinds[k].color, check: true, on: !hiddenKinds[k],
        pick: function () {
          if (hiddenKinds[k]) delete hiddenKinds[k]; else hiddenKinds[k] = true;
          render();
        },
        state: function () { return !hiddenKinds[k]; }
      };
    }));
  });

  // Using the dock keeps the controls up.
  el.addEventListener('pointermove', function () { if (opts.activity) opts.activity(); });

  return {
    el: el, attach: attach, detach: detach, place: place, act: act, key: key, syncFull: syncFull,
    current: function () { return v; },
    time: function () { return v ? v.currentTime : 0; },
    flash: showFlash
  };
}
