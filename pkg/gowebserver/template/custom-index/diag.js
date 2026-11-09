// ===== Diagnostics =====
// With the server's -diagnostics, the page tells it what happened, in
// batches: the thumbnails it loaded or gave up on, the video tiles, the
// filmstrip, what's on screen every few seconds, and script errors. The
// server anonymizes every URL and keeps only the fields it knows (see
// diagnostics.go), so nothing here sends a name on purpose, and anything
// that did would be dropped. Without -diagnostics (CFG.diag is ""), diag
// does nothing.
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
  doc.addEventListener('visibilitychange', function () { if (doc.visibilityState === 'hidden') flush(); });

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

  return function (ev, o) {
    o = o || {};
    o.ev = ev;
    o.pt = Math.round(performance.now() - t0);
    buf.push(o);
    if (buf.length > 300) flush(); else if (!timer) timer = setTimeout(flush, 2000);
  };
})();

// What the browser knows of a fetched URL: status, size, whether it came
// from its cache, and the server's timing.
function diagResource(url) {
  if (!CFG.diag) return {};
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

// The page, once, and then what's on screen every few seconds, when it
// changed: how many tiles there are and how many are still waiting.
(function () {
  if (!CFG.diag) return;
  var proto = '';
  try { proto = performance.getEntriesByType('navigation')[0].nextHopProtocol; } catch (e) { }
  diag('page', {
    page: location.href, n: CFG.media.length, count: CFG.media.filter(function (m) { return m[0]; }).length,
    vw: innerWidth, vh: innerHeight, dpr: devicePixelRatio, proto: proto, thumbs: CFG.thumbs, videoThumbs: CFG.videoThumbs
  });
  var last = '';
  setInterval(function () {
    // A folder with no photos or videos has no grid.
    var grid = doc.getElementById('media');
    if (doc.visibilityState !== 'visible' || !grid) return;
    var s = { tiles: 0, ready: 0, err: 0, plain: 0, loading: 0 };
    grid.querySelectorAll('a').forEach(function (a) {
      var r = a.getBoundingClientRect();
      if (r.bottom < 0 || r.top > innerHeight || !a._item) return;
      s.tiles++;
      if (a.classList.contains('ready')) s.ready++;
      else if (a.classList.contains('err')) s.err++;
      else if (a._loading) s.loading++;
      if (a._plain) s.plain++;
    });
    var st = thumbLoader.stats();
    s.queued = st.queued;
    s.size = st.loading;
    var key = JSON.stringify(s);
    if (key !== last) { last = key; diag('screen', s); }
  }, 5000);
})();
