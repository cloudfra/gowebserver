// TEMPORARY diagnostics, stubbed when util.js is not loaded (tests).
var diag = typeof diag === 'function' ? diag : function () { };
var diagResource = typeof diagResource === 'function' ? diagResource : function () { return {}; };
// ===== Filter =====
(function () {
  var input = $('filter'), timer = 0;
  input.addEventListener('input', function () {
    clearTimeout(timer);
    timer = setTimeout(function () { applyFilter(input.value); }, 120);
  });
  input.addEventListener('keydown', function (e) {
    if (e.key === 'Escape' && input.value) { input.value = ''; applyFilter(''); e.stopPropagation(); }
  });
  doc.addEventListener('keydown', function (e) {
    if (e.key === '/' && !e.metaKey && !e.ctrlKey && doc.activeElement.tagName !== 'INPUT' && !doc.querySelector('dialog[open]')) {
      e.preventDefault();
      input.focus();
    }
  });
})();

applyFilter($('filter').value || '');

// TEMPORARY diagnostics: the page, then what's on screen every 5s.
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
    if (doc.visibilityState !== 'visible') return;
    var s = { tiles: 0, ready: 0, err: 0, plain: 0, loading: 0 };
    mediaEl.querySelectorAll('a').forEach(function (a) {
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
