// ===== Performance log =====
// Added to every page when the server keeps a performance log
// (monitoring.perfLog; see perflog.go). It records how the page behaves
// (frame times, video events, request timings, taps, errors) and posts
// it to __perf, beside this script, every couple of seconds. The "mark"
// button at the bottom left logs a marker, for "it was slow just now".
(function () {
  if (window.__perfLog) return;
  window.__perfLog = true;
  var sid = Math.random().toString(36).slice(2, 10);
  var q = [], marks = 0, vidN = 0;
  // The server's root as the browser sees it: where this script came
  // from, which a proxy may have put under a prefix.
  var src = (document.currentScript && document.currentScript.src) || '';
  var base = src.slice(0, src.lastIndexOf('/') + 1);
  var endpoint = base + '__perf';
  var basePath = (base.match(/^[a-z][a-z0-9+.-]*:\/\/[^/]*(\/.*)$/i) || [0, '/'])[1];
  // The server ties its requests to this page by the cookie.
  document.cookie = 'perfsid=' + sid + '; path=' + basePath + '; SameSite=Lax';

  function now() { return Math.round(performance.now()); }
  function r2(n) { return Math.round(n * 100) / 100; }
  function rec(type, data) {
    data = data || {};
    data.type = type;
    data.ts = now();
    q.push(data);
  }
  // What the log keeps of a URL: its file's extension ("/" for a
  // folder, "?" for one that doesn't look like an extension) and how
  // many folders below the server's root it is. Never the path itself;
  // the server checks this again.
  function shape(u) {
    var p = String(u || '').split(/[?#]/)[0];
    var m = p.match(/^[a-z][a-z0-9+.-]*:\/\/[^/]*(\/.*)?$/i);
    if (m) p = m[1] || '/';
    if (p.indexOf(basePath) === 0) p = p.slice(basePath.length);
    var segs = p.split('/').filter(Boolean);
    if (!segs.length || p.charAt(p.length - 1) === '/') return { ext: '/', depth: segs.length };
    var last = segs[segs.length - 1], dot = last.lastIndexOf('.');
    var ext = dot > 0 ? last.slice(dot).toLowerCase() : '';
    if (!/^(\.[a-z0-9]{1,5})?$/.test(ext)) ext = '?';
    return { ext: ext, depth: segs.length - 1 };
  }
  // Text that may name files (an error's message or stack, the console),
  // with its URLs and paths replaced.
  function redact(s) {
    return String(s).replace(/[a-z][a-z0-9+.-]*:\/\/[^\s"'<>()]*/gi, '<path>')
      .replace(/(^|[\s"'(=:,[])\/[^\s"'<>()]+/g, '$1<path>')
      .replace(/[^\s"'<>()]*\/[^\s"'<>()]*\.[a-z0-9]{1,5}\b/gi, '<path>');
  }
  function describe(el) {
    if (!el || !el.tagName) return '';
    var s = el.tagName.toLowerCase();
    if (el.id) s += '#' + el.id;
    if (typeof el.className === 'string' && el.className) s += '.' + el.className.trim().split(/\s+/).join('.');
    return s;
  }
  // What's on the page: whether a dialog (the slideshow) is open, and
  // how many videos (playing or not) and pictures are in it.
  function context() {
    var videos = document.querySelectorAll('video');
    return {
      dialog: !!document.querySelector('dialog[open]'),
      scrollY: Math.round(scrollY),
      videos: videos.length,
      playing: Array.prototype.filter.call(videos, function (v) { return !v.paused; }).length,
      imgs: document.querySelectorAll('img').length
    };
  }

  function flush(beacon) {
    if (!q.length) return;
    var body = JSON.stringify({ sid: sid, page: shape(location.pathname), events: q });
    q = [];
    if (beacon && navigator.sendBeacon && navigator.sendBeacon(endpoint, body)) return;
    try {
      fetch(endpoint, { method: 'POST', body: body, keepalive: body.length < 60000, headers: { 'Content-Type': 'application/json' } }).catch(function () { });
    } catch (e) { }
  }
  setInterval(flush, 2000);

  // A page iOS killed (out of memory) never gets its pagehide.
  try {
    var prev = sessionStorage.getItem('perf.alive');
    if (prev) rec('unclean-exit', { prevSid: prev });
    sessionStorage.setItem('perf.alive', sid);
  } catch (e) { }
  addEventListener('pagehide', function (e) {
    try { sessionStorage.removeItem('perf.alive'); } catch (err) { }
    rec('pagehide', { persisted: e.persisted });
    flush(true);
  });
  addEventListener('pageshow', function (e) { if (e.persisted) rec('pageshow-bfcache'); });
  document.addEventListener('visibilitychange', function () {
    rec('visibility', { state: document.visibilityState });
    if (document.visibilityState === 'hidden') flush(true);
  });

  // The device. canvasFilter is whether a canvas can blur (the page's
  // backdrop falls back to a CSS blur where it can't, as in Safari).
  var c = navigator.connection, canvasFilter = null;
  try { canvasFilter = 'filter' in document.createElement('canvas').getContext('2d'); } catch (e) { }
  rec('start', {
    ua: navigator.userAgent, platform: navigator.platform, touch: navigator.maxTouchPoints,
    cores: navigator.hardwareConcurrency, mem: navigator.deviceMemory,
    screen: typeof screen === 'undefined' ? '' : screen.width + 'x' + screen.height,
    dpr: devicePixelRatio, view: innerWidth + 'x' + innerHeight,
    net: c ? c.effectiveType + ' ' + c.downlink + 'Mb/s rtt ' + c.rtt : '',
    canvasFilter: canvasFilter,
    finePointer: matchMedia('(pointer: fine)').matches, anyFine: matchMedia('(any-pointer: fine)').matches,
    hover: matchMedia('(hover: hover)').matches
  });
  addEventListener('load', function () {
    setTimeout(function () {
      var n = performance.getEntriesByType('navigation')[0];
      if (n) rec('nav', { proto: n.nextHopProtocol, ttfb: Math.round(n.responseStart - n.requestStart), dcl: Math.round(n.domContentLoadedEventEnd), load: Math.round(n.loadEventEnd), size: n.transferSize, dom: document.querySelectorAll('*').length });
      var amb = document.querySelector('.ambient');
      rec('features', { ambientCssBlur: !!(amb && amb.classList.contains('css-blur')) });
    }, 0);
  });

  // ---- Frames: a summary each second, and every long one ----
  var last = performance.now(), sec = null, lastScroll = scrollY;
  function resetSec() { sec = { frames: 0, max: 0, over34: 0, over50: 0, over100: 0 }; }
  resetSec();
  function frame(t) {
    var d = t - last;
    last = t;
    if (document.visibilityState === 'visible' && d < 10000) {
      sec.frames++;
      if (d > sec.max) sec.max = d;
      if (d > 34) sec.over34++;
      if (d > 50) sec.over50++;
      if (d > 100) sec.over100++;
      if (d > 200) { var cx = context(); cx.ms = Math.round(d); rec('jank', cx); }
    }
    requestAnimationFrame(frame);
  }
  requestAnimationFrame(frame);
  setInterval(function () {
    if (document.visibilityState !== 'visible') { resetSec(); return; }
    var cx = context();
    cx.frames = sec.frames; cx.maxMs = Math.round(sec.max);
    cx.over34 = sec.over34; cx.over50 = sec.over50; cx.over100 = sec.over100;
    cx.scrollDy = Math.round(Math.abs(scrollY - lastScroll));
    lastScroll = scrollY;
    rec('fps', cx);
    resetSec();
  }, 1000);

  // ---- Media ----
  function buffered(v) {
    var out = [];
    try { for (var i = 0; i < v.buffered.length; i++) out.push(r2(v.buffered.start(i)) + '-' + r2(v.buffered.end(i))); } catch (e) { }
    return out.join(',');
  }
  function videoInfo(v) {
    if (!v.__pid) { v.__pid = ++vidN; v.__born = now(); }
    return {
      id: v.__pid, url: shape(v.currentSrc || v.src), age: now() - v.__born, ct: r2(v.currentTime || 0),
      dur: isFinite(v.duration) ? r2(v.duration) : null, rs: v.readyState, ns: v.networkState,
      buf: buffered(v), paused: v.paused, preload: v.preload, w: v.videoWidth, h: v.videoHeight,
      err: v.error ? v.error.code + ' ' + (v.error.message || '') : null,
      where: v.closest('dialog') ? 'dialog' : v.closest('.tile') ? 'tile' : describe(v.parentNode),
      connected: v.isConnected
    };
  }
  ['loadstart', 'loadedmetadata', 'loadeddata', 'canplay', 'canplaythrough', 'play', 'playing', 'waiting',
    'stalled', 'suspend', 'seeking', 'seeked', 'pause', 'ended', 'error', 'abort', 'emptied'].forEach(function (n) {
    document.addEventListener(n, function (e) {
      var v = e.target;
      if (!v || v.tagName !== 'VIDEO') return;
      var info = videoInfo(v);
      info.ev = n;
      rec('video', info);
    }, true);
  });
  // Playing videos: how playback is going.
  setInterval(function () {
    Array.prototype.forEach.call(document.querySelectorAll('video'), function (v) {
      if (v.paused || !v.getVideoPlaybackQuality) return;
      var pq = v.getVideoPlaybackQuality(), info = videoInfo(v);
      info.dropped = pq.droppedVideoFrames;
      info.total = pq.totalVideoFrames;
      info.dDropped = pq.droppedVideoFrames - (v.__dropped || 0);
      info.dTotal = pq.totalVideoFrames - (v.__total || 0);
      v.__dropped = pq.droppedVideoFrames;
      v.__total = pq.totalVideoFrames;
      rec('vq', info);
    });
  }, 2000);

  // ---- Resources ----
  try { performance.setResourceTimingBufferSize(100000); } catch (e) { }
  try {
    new PerformanceObserver(function (list) {
      list.getEntries().forEach(function (en) {
        if (en.name.indexOf('/__perf') >= 0) return;
        var d = {
          n: shape(en.name), it: en.initiatorType, st: Math.round(en.startTime), dur: Math.round(en.duration),
          ttfb: en.responseStart ? Math.round(en.responseStart - en.startTime) : null,
          dl: en.responseEnd && en.responseStart ? Math.round(en.responseEnd - en.responseStart) : null,
          size: en.transferSize, body: en.encodedBodySize, proto: en.nextHopProtocol
        };
        if (en.serverTiming && en.serverTiming.length) {
          d.srv = en.serverTiming.map(function (s) { return s.name + '=' + r2(s.duration); }).join(' ');
        }
        rec('res', d);
      });
    }).observe({ type: 'resource', buffered: true });
  } catch (e) { rec('unsupported', { what: 'resource timing', msg: String(e) }); }

  // ---- Errors and console ----
  addEventListener('error', function (e) {
    if (e.target && e.target !== window && e.target.tagName) {
      rec('load-error', { el: describe(e.target), url: shape(e.target.currentSrc || e.target.src) });
      return;
    }
    rec('err', { msg: redact(e.message), file: shape(e.filename), line: e.lineno + ':' + e.colno, stack: e.error && e.error.stack ? redact(String(e.error.stack).slice(0, 2000)) : '' });
  }, true);
  addEventListener('unhandledrejection', function (e) {
    var r = e.reason;
    rec('err', { msg: redact('unhandled rejection: ' + (r && r.message || r)), stack: r && r.stack ? redact(String(r.stack).slice(0, 2000)) : '' });
  });
  ['error', 'warn'].forEach(function (k) {
    var orig = console[k];
    console[k] = function () {
      try { rec('console', { level: k, msg: redact(Array.prototype.map.call(arguments, String).join(' ').slice(0, 2000)) }); } catch (e) { }
      return orig.apply(console, arguments);
    };
  });

  // ---- Taps: what was touched, and how long until the next frame ----
  ['pointerdown', 'click'].forEach(function (n) {
    document.addEventListener(n, function (e) {
      if (e.target && e.target.id === '__perfmark') return;
      var t0 = performance.now();
      var d = { ev: n, pt: e.pointerType, target: describe(e.target), tile: !!(e.target.closest && e.target.closest('.tile')) };
      requestAnimationFrame(function () {
        d.handlerMs = Math.round(t0 - e.timeStamp);
        d.nextFrameMs = Math.round(performance.now() - e.timeStamp);
        rec('tap', d);
      });
    }, true);
  });

  // ---- The mark button ----
  function addButton() {
    var b = document.createElement('button');
    b.id = '__perfmark';
    b.type = 'button';
    b.textContent = 'mark';
    b.style.cssText = 'position:fixed;left:8px;bottom:8px;z-index:2147483647;padding:8px 12px;border:0;border-radius:8px;' +
      'background:rgba(200,30,30,.75);color:#fff;font:600 13px system-ui;';
    b.addEventListener('click', function (e) {
      e.stopPropagation();
      marks++;
      var cx = context();
      cx.n = marks;
      rec('mark', cx);
      flush();
      b.textContent = 'mark ' + marks + ' ✓';
    }, true);
    // A modal dialog (the slideshow) and full screen cover everything
    // else, so the button moves into whichever is showing.
    function home() {
      return document.fullscreenElement || document.webkitFullscreenElement ||
        document.querySelector('dialog[open]') || document.body;
    }
    function rehome() {
      var h = home();
      if (b.parentNode !== h) h.appendChild(b);
    }
    rehome();
    setInterval(rehome, 300);
  }
  if (document.body) addButton(); else addEventListener('DOMContentLoaded', addButton);
})();
