// ===== Slideshow =====
var slideshow = (function () {
  var dlg = $('ss'), stage = $('ss-stage');
  var frame = $('ss-frame'), btnAuto = $('ss-auto'), btnFull = $('ss-full'), btnOpen = $('ss-open'), btnDl = $('ss-dl');
  var btnPrev = $('ss-prev'), btnNext = $('ss-next'), strip = $('ss-strip');
  var ambient = Ambient($('ss-ambient'), function (src) { accent.from('ss', [{ el: src, w: 1 }]); });
  var RADIUS = 1, GAP = 24, AUTO_MS = 5000, STRIP_RADIUS = 12;

  var list = [], idx = 0, slides = new Map(), isOpen = false, closing = false;
  var W = 0, H = 0;
  var dx = 0;               // horizontal drag offset
  var zoom = { z: 1, x: 0, y: 0 };
  var auto = false, autoTimer = 0, chromeTimer = 0;

  var fullscreenEnabled = doc.fullscreenEnabled || doc.webkitFullscreenEnabled;
  btnFull.hidden = !fullscreenEnabled;

  function isFullscreen() {
    return !!(doc.fullscreenElement || doc.webkitFullscreenElement);
  }

  // Must run inside a click or key press; browsers only grant
  // fullscreen during a user gesture. A refusal leaves the slideshow
  // open in the window. The frame goes fullscreen, not the dialog:
  // requestFullscreen always rejects on a <dialog>.
  function enterFullscreen() {
    if (!fullscreenEnabled || isFullscreen()) return;
    try {
      var p = (frame.requestFullscreen || frame.webkitRequestFullscreen).call(frame);
      if (p && p.catch) p.catch(function () { });
    } catch (e) { }
  }

  function exitFullscreen() {
    if (isFullscreen()) (doc.exitFullscreen || doc.webkitExitFullscreen).call(doc);
  }

  // Starting autoplay goes fullscreen unless the viewer turned that
  // off. Only the fullscreen button (or F) during autoplay changes the
  // preference: leaving with Esc doesn't, since people press Esc on
  // the way to closing the slideshow.
  var FULLSCREEN_KEY = 'gows-slideshow-fullscreen';
  function wantsFullscreen() {
    return store(FULLSCREEN_KEY) !== 'off';
  }

  // The button enters full screen fitted (inner), then each press
  // moves on: outer (fill the screen), and out of full screen. Its
  // icon shows the mode, and its label what the next press does.
  var FIT_MODES = ['inner', 'outer'], fitMode = 'inner';
  var FULL_ICONS = {
    window: '<svg viewBox="0 0 24 24"><path class="i-tl" d="M4 9V4h5"/><path class="i-tr" d="M20 9V4h-5"/><path class="i-bl" d="M4 15v5h5"/><path class="i-br" d="M20 15v5h-5"/></svg>',
    inner: '<svg viewBox="0 0 24 24"><g class="i-in"><path d="M9 4v5H4"/><path d="M15 4v5h5"/><path d="M9 20v-5H4"/><path d="M15 20v-5h5"/></g></svg>',
    outer: '<svg viewBox="0 0 24 24"><g class="i-fill"><path class="i-l" d="M3 9v6"/><path class="i-r" d="M21 9v6"/>' +
      '<path class="i-t" d="M6 6h12"/><path class="i-b" d="M6 18h12"/></g></svg>'
  };
  var FULL_LABELS = {
    window: 'Full screen',
    inner: 'Fill the screen',
    outer: 'Exit full screen'
  };

  function toggleFullscreen() {
    if (!isFullscreen()) {
      fitMode = FIT_MODES[0];
      enterFullscreen();
      if (auto) store(FULLSCREEN_KEY, null);
      return;
    }
    var next = FIT_MODES.indexOf(fitMode) + 1;
    if (next < FIT_MODES.length) {
      fitMode = FIT_MODES[next];
      syncFullscreenButton();
      relayoutSlides();
      return;
    }
    exitFullscreen();
    if (auto) store(FULLSCREEN_KEY, 'off');
  }

  var shownFull = null;
  function syncFullscreenButton() {
    var on = isFullscreen();
    if (!on) fitMode = FIT_MODES[0];
    var state = on ? fitMode : 'window';
    btnFull.setAttribute('aria-label', FULL_LABELS[state]);
    if (shownFull !== state) {
      btnFull.innerHTML = FULL_ICONS[state];
      shownFull = state;
    }
    dlg.classList.toggle('is-fs', on);
    if (!on) hideStrip();
  }
  // Entering or leaving full screen changes the room the media has.
  doc.addEventListener('fullscreenchange', function () { if (isOpen) relayoutSlides(); });
  doc.addEventListener('webkitfullscreenchange', function () { if (isOpen) relayoutSlides(); });
  doc.addEventListener('fullscreenchange', syncFullscreenButton);
  doc.addEventListener('webkitfullscreenchange', syncFullscreenButton);

  function measure() {
    W = stage.clientWidth;
    H = stage.clientHeight;
  }

  // Space reserved around a slide for the controls; phones go edge to edge.
  function insets() {
    // In full screen the controls float over the media instead.
    if (W < 700 || isFullscreen()) return { t: 0, r: 0, b: 0, l: 0 };
    return { t: 76, r: 96, b: CFG.thumbs ? 104 : 64, l: 96 };
  }

  function fitRect(nw, nh, maxUp) {
    var p = insets();
    var bw = W - p.l - p.r, bh = H - p.t - p.b;
    // Outer covers the screen, whatever the size.
    var f = isFullscreen() && fitMode !== 'inner' ?
      Math.max(bw / nw, bh / nh) : Math.min(bw / nw, bh / nh, maxUp);
    var w = nw * f, h = nh * f;
    return { w: w, h: h, x: p.l + (bw - w) / 2, y: p.t + (bh - h) / 2 };
  }

  function sizeMedia(el) {
    var nw = el.naturalWidth || el.videoWidth, nh = el.naturalHeight || el.videoHeight;
    if (!nw || !nh) {
      if (el.tagName === 'VIDEO') { el.style.width = Math.min(W, 960) + 'px'; el.style.height = Math.min(H, 540) + 'px'; }
      return;
    }
    var r = fitRect(nw, nh, el._thumb ? Infinity : 2);
    el.style.width = r.w + 'px';
    el.style.height = r.h + 'px';
  }

  function makeSlide(k) {
    var it = list[k];
    var div = doc.createElement('div');
    div.className = 'slide';
    var p = insets();
    div.style.padding = p.t + 'px ' + p.r + 'px ' + p.b + 'px ' + p.l + 'px';
    if (it.video) {
      var v = doc.createElement('video');
      v.controls = true;
      v.playsInline = true;
      v.preload = 'metadata';
      v.addEventListener('loadedmetadata', function () {
        sizeMedia(v);
        if (auto && list[idx] === it) v.play().catch(function () { });
      });
      v.addEventListener('loadeddata', function () { if (list[idx] === it) ambient.show(v); });
      v.addEventListener('ended', function () { if (auto && list[idx] === it) step(1, true); });
      if (CFG.videoThumbs) v.poster = previewURL(it);
      // A neighbor's video waits for the current slide (loadNeighbors).
      if (k === idx) v.src = it.url; else v._pendingSrc = it.url;
      sizeMedia(v);
      div.appendChild(v);
    } else {
      var img = doc.createElement('img');
      img.alt = it.name;
      img.decoding = 'async';
      img.draggable = false;
      // The slide being opened loads first; its neighbors wait.
      img.fetchPriority = k === idx ? 'high' : 'low';
      var preview = previewURL(it);
      img._thumb = preview !== it.url;
      img._box = PREVIEW_PX;
      img.onload = function () {
        img.onload = null;
        settleSize(img);
        sizeMedia(img);
        img._amb = ambientSource(img);
        img.classList.add('ready');
        if (list[idx] === it) {
          ambient.show(img._amb);
          upgrade(div, it);
        }
      };
      img.src = preview;
      div.appendChild(img);
    }
    div._k = k;
    return div;
  }

  // ---- Sharper versions of the current photo ----
  // A slide first shows the preview (PREVIEW_PX, shared by every
  // screen). The current slide then upgrades to the smallest version
  // that fills it on this screen: nothing more if the preview does, a
  // SHARP_PX server thumbnail if that does, else the original. The
  // original (often many megabytes and tens of megapixels) otherwise
  // loads only on zooming in. Each version is decoded off the main
  // thread before it replaces the one on screen, so the swap is
  // instant, and a download is dropped when its slide stops being
  // current.
  var SHARP_PX = 2048;

  // A thumbnail smaller than its box in both directions wasn't
  // shrunk (inside never enlarges): it's the original's full size, so
  // it's sized like the original, never blown up past twice.
  function settleSize(img) {
    if (img._thumb && img.naturalWidth < img._box && img.naturalHeight < img._box) img._thumb = false;
  }

  function upgrade(div, it, original) {
    var img = div && div.firstChild;
    if (!img || img.tagName !== 'IMG' || !img._thumb) return;
    if (div._pending) {
      // The original takes over from a smaller version on its way.
      if (!original || div._pending._original) return;
      cancelUpgrade(div);
    }
    var target;
    if (original) {
      target = { url: it.url, box: Infinity };
    } else {
      var shown = fitRect(img.naturalWidth, img.naturalHeight, Infinity);
      var need = Math.max(shown.w, shown.h) * Math.min(window.devicePixelRatio || 1, 3);
      if (need <= img._box) return;
      target = need <= SHARP_PX && img._box < SHARP_PX ?
        { url: it.url + '?width=' + SHARP_PX + '&height=' + SHARP_PX + '&fit=inside', box: SHARP_PX } :
        { url: it.url, box: Infinity };
    }
    var hi = new Image();
    hi.alt = img.alt;
    hi.draggable = false;
    hi.fetchPriority = 'high';
    hi._original = target.box === Infinity;
    div._pending = hi;
    hi.src = target.url;
    var swap = function () {
      if (div._pending !== hi) return;
      div._pending = null;
      var cur = div.firstChild;
      if (!cur || !cur.isConnected) return;
      hi._thumb = target.box !== Infinity;
      hi._box = target.box;
      settleSize(hi);
      hi._amb = cur._amb;
      hi.className = cur.className;
      hi.style.cssText = cur.style.cssText;
      div.replaceChild(hi, cur);
      cur.removeAttribute('src');
      sizeMedia(hi);
      // Don't shrink a zoomed photo out from under the viewer.
      if (slides.get(idx) === div) applyZoom(false);
    };
    // decode() can reject for a loaded image (some browsers do for
    // very large ones); it still swaps in, decoded when painted.
    hi.decode().then(swap, function () {
      if (hi.complete && hi.naturalWidth) swap();
      else if (div._pending === hi) div._pending = null;
    });
  }

  function cancelUpgrade(div) {
    if (!div || !div._pending) return;
    div._pending.removeAttribute('src');
    div._pending = null;
  }

  // A tiny copy of a photo for the backdrop and accent color, drawn
  // once from the preview. Drawing the on-screen image each time
  // would redraw (and possibly first decode) the full original on the
  // main thread, mid-transition.
  function ambientSource(img) {
    var c = doc.createElement('canvas');
    c.width = 32;
    c.height = 24;
    try {
      c.getContext('2d').drawImage(img, 0, 0, c.width, c.height);
    } catch (e) {
      return img;
    }
    return c;
  }

  function unmountSlide(div) {
    cancelUpgrade(div);
    var m = div.firstChild;
    if (m && m.tagName === 'VIDEO') { m.pause(); m.removeAttribute('src'); m.load(); }
    else if (m) m.removeAttribute('src');
    div.remove();
  }

  // Only the current slide and its neighbors exist, so memory stays
  // flat no matter how many items the folder has.
  function mountWindow() {
    slides.forEach(function (div, k) {
      if (Math.abs(k - idx) > RADIUS) { unmountSlide(div); slides.delete(k); }
    });
    for (var k = Math.max(0, idx - RADIUS); k <= Math.min(list.length - 1, idx + RADIUS); k++) {
      if (!slides.has(k)) {
        var div = makeSlide(k);
        div.style.transform = 'translate3d(' + ((k - idx) * (W + GAP)) + 'px,0,0)';
        slides.set(k, div);
        stage.appendChild(div);
      }
    }
    var cur = slides.get(idx);
    var m = cur && cur.firstChild;
    if (m && m._pendingSrc) loadVideo(m);
    loadNeighborsAfter(m);
    if (m && m.tagName === 'IMG' && m.classList.contains('ready')) { ambient.show(m._amb || m); upgrade(cur, list[idx]); }
    // The new current slide's preview, if still loading, jumps the
    // queue ahead of its neighbors'.
    if (m && m.tagName === 'IMG') m.fetchPriority = 'high';
    if (m && m.tagName === 'VIDEO' && m.readyState >= 2) ambient.show(m);
  }

  function loadVideo(v) {
    v.src = v._pendingSrc;
    v._pendingSrc = null;
  }

  // The neighbors' videos load once the current slide's video shows a
  // frame (or soon after, if it doesn't): a video starting up is held
  // back by every other one loading beside it, and on an iPad by a lot.
  // A photo's neighbors load at once.
  var NEIGHBORS_AFTER = 3000, neighborsTimer = 0, neighborsWait = null;
  function loadNeighborsAfter(m) {
    clearTimeout(neighborsTimer);
    if (neighborsWait) neighborsWait.removeEventListener('loadeddata', loadNeighbors);
    neighborsWait = null;
    if (!m || m.tagName !== 'VIDEO' || m.readyState >= 2) { loadNeighbors(); return; }
    neighborsWait = m;
    m.addEventListener('loadeddata', loadNeighbors, { once: true });
    neighborsTimer = setTimeout(loadNeighbors, NEIGHBORS_AFTER);
  }

  function loadNeighbors() {
    clearTimeout(neighborsTimer);
    if (neighborsWait) neighborsWait.removeEventListener('loadeddata', loadNeighbors);
    neighborsWait = null;
    slides.forEach(function (div) {
      var v = div.firstChild;
      if (v && v._pendingSrc) loadVideo(v);
    });
  }

  function position(ms) {
    slides.forEach(function (div, k) {
      div.style.transition = ms ? 'transform ' + ms + 'ms var(--ease)' : 'none';
      div.style.transform = 'translate3d(' + ((k - idx) * (W + GAP) + dx) + 'px,0,0)';
    });
  }

  function currentMedia() {
    var div = slides.get(idx);
    return div && div.firstChild;
  }

  function updateInfo() {
    var it = list[idx];
    $('ss-name').textContent = it.name;
    $('ss-sub').innerHTML = '<span>' + numFmt.format(idx + 1) + ' of ' + numFmt.format(list.length) + '</span>' +
      '<span>' + fmtBytes(it.size) + '</span><span>' + dateFmt.format(new Date(it.t * 1000)) + '</span>';
    btnOpen.href = it.url;
    btnDl.href = it.url;
    btnDl.setAttribute('download', it.name);
    btnPrev.disabled = idx === 0;
    btnNext.disabled = idx === list.length - 1;
    renderStrip();
  }

  // Swaps a filmstrip thumbnail that didn't load for the placeholder.
  function stripThumbDone(b) {
    return function (ok) {
      if (ok) { b.classList.add('ready'); return; }
      b.textContent = '';
      b._img = null;
      b.classList.remove('vid');
      b.classList.add('err');
    };
  }

  // A filmstrip thumbnail with the gap to the next (.ss-strip in the
  // stylesheet), and the padding either side of the strip.
  var STRIP_STEP = 60, STRIP_MARGIN = 28;
  function renderStrip() {
    if (!CFG.thumbs) return;
    strip.hidden = list.length < 2;
    // As many either side of the current one as fit across the
    // window, up to STRIP_RADIUS.
    var radius = Math.max(1, Math.min(STRIP_RADIUS, Math.floor(((innerWidth - STRIP_MARGIN) / STRIP_STEP - 1) / 2)));
    var lo = Math.max(0, idx - radius), hi = Math.min(list.length - 1, idx + radius);
    var keep = {};
    Array.prototype.slice.call(strip.children).forEach(function (b) {
      if (b._k < lo || b._k > hi) { if (b._img) thumbLoader.cancel(b._img); b.remove(); }
      else keep[b._k] = b;
    });
    var before = strip.firstChild;
    for (var k = lo; k <= hi; k++) {
      var b = keep[k];
      if (!b) {
        b = doc.createElement('button');
        b.type = 'button';
        b._k = k;
        b.setAttribute('aria-label', list[k].name);
        // A video's play icon is its placeholder until the thumbnail
        // fades in over it (a photo's is the picture icon, in CSS).
        if (list[k].video) b.classList.add('vid');
        if (list[k].video && !CFG.videoThumbs) {
          b.innerHTML = svgUse('ui-play');
        } else {
          b.innerHTML = (list[k].video ? svgUse('ui-play') : '') + '<img alt="" decoding="async" fetchpriority="low">';
          b._img = b.lastChild;
          // Ahead of the grid's, which the slideshow covers.
          thumbLoader.load(b._img, tileThumbURL(list[k], 0), stripThumbDone(b), true);
        }
      }
      if (b !== before) strip.insertBefore(b, before); else before = before.nextSibling;
      if (k === idx) b.setAttribute('aria-current', 'true'); else b.removeAttribute('aria-current');
    }
  }

  strip.addEventListener('click', function (e) {
    var b = e.target.closest('button');
    if (b) go(b._k, 420);
  });

  // ---- Strip in full screen ----
  // Hidden until the pointer or a finger comes within STRIP_ZONE px
  // of the bottom edge. It goes again shortly after the mouse leaves
  // that area, or a while after a touch.
  var STRIP_ZONE = 120, stripTimer = 0;
  function revealStrip(hideAfter) {
    dlg.classList.add('strip-on');
    clearTimeout(stripTimer);
    stripTimer = hideAfter ? setTimeout(hideStrip, hideAfter) : 0;
  }
  function hideStrip() {
    clearTimeout(stripTimer);
    stripTimer = 0;
    dlg.classList.remove('strip-on');
  }
  function stripZone(e) {
    if (!isFullscreen() || strip.hidden) return;
    var near = e.clientY > frame.getBoundingClientRect().bottom - STRIP_ZONE;
    if (e.pointerType !== 'mouse') {
      if (near) revealStrip(3000);
    } else if (near) {
      revealStrip(0);
    } else if (dlg.classList.contains('strip-on') && !stripTimer) {
      stripTimer = setTimeout(hideStrip, 400);
    }
  }
  dlg.addEventListener('pointermove', stripZone);
  dlg.addEventListener('pointerdown', stripZone);

  function durationFor(v) {
    return Math.round(420 - Math.min(Math.abs(v || 0), 2) * 110);
  }

  function go(k, ms) {
    if (k < 0 || k >= list.length) { dx = 0; position(320); return; }
    var prev = currentMedia();
    if (prev && prev.tagName === 'VIDEO') prev.pause();
    resetZoom(false);
    // Stepping on drops the sharper download of the slide being left,
    // so quick steps don't pile up big downloads ahead of the next.
    cancelUpgrade(slides.get(idx));
    idx = k;
    dx = 0;
    mountWindow();
    position(ms === undefined ? 420 : ms);
    updateInfo();
    playCurrentVideo();
    if (auto) scheduleAuto();
  }

  function playCurrentVideo() {
    var m = currentMedia();
    if (m && m.tagName === 'VIDEO') m.play().catch(function () { });
  }

  function step(d, wrap) {
    var k = idx + d;
    if (wrap && k >= list.length) k = 0;
    go(k);
  }

  // ---- Zoom ----
  function applyZoom(animate) {
    var m = currentMedia();
    if (!m || m.tagName !== 'IMG') return;
    m.style.transition = animate ? 'transform 0.32s var(--ease)' : 'none';
    m.style.transform = zoom.z === 1 && !zoom.x && !zoom.y ? '' : 'translate3d(' + zoom.x + 'px,' + zoom.y + 'px,0) scale(' + zoom.z + ')';
    var s = slides.get(idx);
    if (s) s.classList.toggle('zoomed', zoom.z > 1);
  }

  function resetZoom(animate) {
    zoom = { z: 1, x: 0, y: 0 };
    applyZoom(animate);
  }

  function clampPan() {
    var m = currentMedia();
    if (!m) return;
    var w = parseFloat(m.style.width) * zoom.z, h = parseFloat(m.style.height) * zoom.z;
    var mx = Math.max(0, (w - W) / 2), my = Math.max(0, (h - H) / 2);
    // Offsets are relative to the slide's own center, which sits
    // off the stage center when the insets are uneven.
    zoom.x = Math.max(-mx, Math.min(mx, zoom.x));
    zoom.y = Math.max(-my, Math.min(my, zoom.y));
  }

  // Zoom to z keeping the screen point (px, py) fixed.
  function zoomAt(z, px, py, base, animate) {
    var m = currentMedia();
    if (!m || m.tagName !== 'IMG' || !m.classList.contains('ready')) return;
    base = base || zoom;
    z = Math.max(1, Math.min(8, z));
    var r = m.getBoundingClientRect();
    var cx = r.left + r.width / 2 - zoom.x, cy = r.top + r.height / 2 - zoom.y;
    zoom = {
      z: z,
      x: px - cx - (px - cx - base.x) * z / base.z,
      y: py - cy - (py - cy - base.y) * z / base.z
    };
    if (z === 1) { zoom.x = 0; zoom.y = 0; }
    clampPan();
    applyZoom(animate);
    // Zoomed in, the detail is worth the original's download.
    if (zoom.z > 1.2) upgrade(slides.get(idx), list[idx], true);
  }

  // ---- Gestures (one Pointer Events path for touch, pen and mouse) ----
  var pointers = new Map(), g = null, pinch = null, lastTap = null, tapTimer = 0;

  stage.addEventListener('pointerdown', function (e) {
    if (e.target.tagName === 'VIDEO' && e.pointerType === 'mouse') return;
    if (e.button > 0) return;
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
    if (pointers.size === 2) {
      var p = Array.from(pointers.values());
      pinch = {
        d: Math.hypot(p[0].x - p[1].x, p[0].y - p[1].y),
        mx: (p[0].x + p[1].x) / 2, my: (p[0].y + p[1].y) / 2,
        base: { z: zoom.z, x: zoom.x, y: zoom.y }
      };
      if (g && g.mode === 'swipe') { dx = 0; position(250); }
      g = null;
      return;
    }
    if (pointers.size > 2) return;
    g = { mode: 'pending', x0: e.clientX, y0: e.clientY, t0: performance.now(), lx: e.clientX, ly: e.clientY, lt: performance.now(), vx: 0, vy: 0, zx: zoom.x, zy: zoom.y, type: e.pointerType };
  });

  stage.addEventListener('pointermove', function (e) {
    if (!pointers.has(e.pointerId)) return;
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
    if (pinch && pointers.size === 2) {
      var p = Array.from(pointers.values());
      var d = Math.hypot(p[0].x - p[1].x, p[0].y - p[1].y);
      var mx = (p[0].x + p[1].x) / 2, my = (p[0].y + p[1].y) / 2;
      zoomAt(pinch.base.z * d / pinch.d, pinch.mx, pinch.my, pinch.base, false);
      zoom.x += mx - pinch.mx; zoom.y += my - pinch.my;
      clampPan();
      applyZoom(false);
      return;
    }
    if (!g) return;
    var now = performance.now(), dt = Math.max(1, now - g.lt);
    g.vx = g.vx * 0.3 + ((e.clientX - g.lx) / dt) * 0.7;
    g.vy = g.vy * 0.3 + ((e.clientY - g.ly) / dt) * 0.7;
    g.lx = e.clientX; g.ly = e.clientY; g.lt = now;
    var mx2 = e.clientX - g.x0, my2 = e.clientY - g.y0;
    if (g.mode === 'pending' && Math.hypot(mx2, my2) > 8) {
      if (zoom.z > 1) g.mode = 'pan';
      else if (Math.abs(mx2) >= Math.abs(my2)) g.mode = 'swipe';
      else if (my2 > 0) g.mode = 'dismiss';
      else g.mode = 'none';
      try { stage.setPointerCapture(e.pointerId); } catch (err) { }
      showChrome();
    }
    if (g.mode === 'swipe') {
      var atEdge = (mx2 > 0 && idx === 0) || (mx2 < 0 && idx === list.length - 1);
      dx = atEdge ? mx2 / (1 + Math.abs(mx2) / (W * 0.25)) : mx2;
      position(0);
    } else if (g.mode === 'pan') {
      zoom.x = g.zx + mx2; zoom.y = g.zy + my2;
      clampPan();
      applyZoom(false);
    } else if (g.mode === 'dismiss') {
      var f = Math.min(1, Math.max(0, my2) / H);
      var cur = slides.get(idx);
      if (cur) {
        cur.style.transition = 'none';
        cur.style.transform = 'translate3d(' + mx2 * 0.5 + 'px,' + my2 + 'px,0) scale(' + (1 - f * 0.35) + ')';
      }
      dlg.querySelector('.ss-bg').style.opacity = String(1 - f * 1.5);
    }
  });

  function pointerEnd(e) {
    if (!pointers.has(e.pointerId)) return;
    pointers.delete(e.pointerId);
    if (pinch) {
      if (pointers.size < 2) {
        pinch = null;
        if (zoom.z < 1.05) resetZoom(true);
      }
      return;
    }
    if (!g) return;
    var gs = g;
    g = null;
    var mx = e.clientX - gs.x0, my = e.clientY - gs.y0;
    if (gs.mode === 'swipe') {
      if ((mx < -W * 0.18 || gs.vx < -0.45) && idx < list.length - 1) go(idx + 1, durationFor(gs.vx));
      else if ((mx > W * 0.18 || gs.vx > 0.45) && idx > 0) go(idx - 1, durationFor(gs.vx));
      else { dx = 0; position(320); }
    } else if (gs.mode === 'dismiss') {
      if (my > H * 0.18 || gs.vy > 0.6) requestClose();
      else {
        dlg.querySelector('.ss-bg').style.opacity = '';
        position(320);
      }
    } else if (gs.mode === 'pan') {
      clampPan();
      applyZoom(true);
    } else if (gs.mode === 'pending' && e.type === 'pointerup') {
      tap(e.clientX, e.clientY, gs.type, e.target);
    }
  }
  stage.addEventListener('pointerup', pointerEnd);
  stage.addEventListener('pointercancel', pointerEnd);

  function tap(x, y, type, target) {
    var now = performance.now();
    if (lastTap && now - lastTap.t < 300 && Math.hypot(x - lastTap.x, y - lastTap.y) < 30) {
      clearTimeout(tapTimer);
      lastTap = null;
      if (zoom.z > 1) resetZoom(true); else zoomAt(2.5, x, y, null, true);
      return;
    }
    lastTap = { t: now, x: x, y: y };
    clearTimeout(tapTimer);
    tapTimer = setTimeout(function () {
      if (type !== 'mouse') toggleChrome();
      else if (target === stage || target.classList.contains('slide')) requestClose();
    }, 300);
  }

  // Wheel: pinch-zoom on trackpads (ctrlKey) and scroll-zoom on mice;
  // horizontal trackpad swipes page between items, one item per
  // swipe. A swipe's wheel events keep coming as momentum for a
  // second or more after the fingers lift, so once a swipe has paged,
  // the rest of its events are ignored until they stop for a moment.
  var WHEEL_GESTURE_GAP = 200;
  var wheelAcc = 0, wheelSpent = false, wheelIdle = 0;
  stage.addEventListener('wheel', function (e) {
    e.preventDefault();
    showChrome();
    if (!e.ctrlKey && Math.abs(e.deltaX) > Math.abs(e.deltaY) && zoom.z === 1) {
      clearTimeout(wheelIdle);
      wheelIdle = setTimeout(function () { wheelSpent = false; wheelAcc = 0; }, WHEEL_GESTURE_GAP);
      if (wheelSpent) return;
      wheelAcc += e.deltaX;
      if (Math.abs(wheelAcc) > 60) {
        step(wheelAcc > 0 ? 1 : -1);
        wheelAcc = 0;
        wheelSpent = true;
      }
      return;
    }
    var factor = Math.exp(-e.deltaY * (e.ctrlKey ? 0.01 : 0.0022));
    zoomAt(zoom.z * factor, e.clientX, e.clientY, null, false);
  }, { passive: false });

  // ---- Chrome (controls) visibility ----
  function showChrome() {
    dlg.classList.remove('chrome-off');
    clearTimeout(chromeTimer);
    chromeTimer = setTimeout(function () {
      if (!dlg.querySelector('.ss-top:hover, .ss-bottom:hover, .ss-nav:hover')) dlg.classList.add('chrome-off');
    }, 2800);
  }

  function toggleChrome() {
    clearTimeout(chromeTimer);
    dlg.classList.toggle('chrome-off');
  }

  dlg.addEventListener('pointermove', function (e) { if (e.pointerType === 'mouse') showChrome(); });

  // ---- Autoplay ----
  function scheduleAuto() {
    clearTimeout(autoTimer);
    var m = currentMedia();
    if (m && m.tagName === 'VIDEO') {
      m.play().catch(function () { });
      return;
    }
    autoTimer = setTimeout(function () { step(1, true); }, AUTO_MS);
  }

  function setAuto(on) {
    var starting = on && !auto;
    auto = on;
    if (starting && wantsFullscreen()) enterFullscreen();
    btnAuto.setAttribute('aria-pressed', String(on));
    btnAuto.setAttribute('aria-label', on ? 'Pause slideshow' : 'Play slideshow');
    btnAuto.innerHTML = on
      ? '<svg viewBox="0 0 24 24"><line class="i-bar" x1="9" y1="5" x2="9" y2="19"/><line class="i-bar i-bar2" x1="15" y1="5" x2="15" y2="19"/></svg>'
      : '<svg viewBox="0 0 24 24"><polygon class="i-play" points="7 4 19 12 7 20 7 4"/></svg>';
    dlg.classList.toggle('auto', on);
    if (on) scheduleAuto(); else clearTimeout(autoTimer);
  }

  // ---- Fly animation between a grid tile and the slide ----
  // Keyframes that shrink box (the media's on-screen rect) onto the
  // tile: scaled to cover it, then clipped to its shape.
  function flyRects(tile, box) {
    var t = tile.getBoundingClientRect();
    var f = { w: box.width, h: box.height }, fx = box.left, fy = box.top;
    var scale = Math.max(t.width / f.w, t.height / f.h);
    var ix = (f.w - t.width / scale) / 2, iy = (f.h - t.height / scale) / 2;
    var r = 6 / scale;
    return {
      box: box,
      from: {
        transform: 'translate(' + (t.left + t.width / 2 - (fx + f.w / 2)) + 'px,' + (t.top + t.height / 2 - (fy + f.h / 2)) + 'px) scale(' + scale + ')',
        clipPath: 'inset(' + iy + 'px ' + ix + 'px round ' + r + 'px)'
      },
      to: { transform: 'translate(0,0) scale(1)', clipPath: 'inset(0px 0px round 3px)' }
    };
  }

  function flyEl(src, box) {
    var fly = doc.createElement('img');
    fly.className = 'ss-fly';
    fly.alt = '';
    fly.src = src;
    fly.style.left = box.left + 'px';
    fly.style.top = box.top + 'px';
    fly.style.width = box.width + 'px';
    fly.style.height = box.height + 'px';
    dlg.appendChild(fly);
    return fly;
  }

  function canFly(tile) {
    var img = tile && tile.querySelector('img');
    return !reducedMotion.matches && img && tile.classList.contains('ready') && img.naturalWidth && img.animate ? img : null;
  }

  function flyIn(tile) {
    var img = canFly(tile);
    if (!img) return;
    var f = fitRect(img.naturalWidth, img.naturalHeight, CFG.thumbs ? Infinity : 2);
    var s = stage.getBoundingClientRect();
    var rects = flyRects(tile, { left: s.left + f.x, top: s.top + f.y, width: f.w, height: f.h });
    var fly = flyEl(img.currentSrc || img.src, rects.box);
    tile.classList.add('flying');
    var slideImg = currentMedia();
    if (slideImg) slideImg.style.visibility = 'hidden';
    var anim = fly.animate([rects.from, rects.to], { duration: 420, easing: 'cubic-bezier(0.2, 0.9, 0.2, 1)' });
    anim.onfinish = anim.oncancel = function () {
      tile.classList.remove('flying');
      var m = currentMedia();
      var finish = function () { if (m) m.style.visibility = ''; fly.remove(); };
      // Keep the (already decoded) grid image up until the slide's own
      // image is ready, so there is never an empty frame.
      if (m && m.tagName === 'IMG' && !m.classList.contains('ready')) {
        m.style.visibility = '';
        m.addEventListener('load', function () { setTimeout(finish, 60); }, { once: true });
        setTimeout(finish, 4000);
      } else if (m && m.tagName === 'VIDEO' && m.readyState < 1) {
        // A video has a placeholder size until its metadata says
        // how big it is; stay on the thumbnail until then.
        m.addEventListener('loadedmetadata', function () { setTimeout(finish, 60); }, { once: true });
        setTimeout(finish, 4000);
      } else finish();
    };
  }

  function flyOut(m, box, done) {
    var it = list[idx];
    var tile = !it.video && grid ? grid.reveal(it) : null;
    if (!canFly(tile) || !m || m.tagName !== 'IMG' || !m.classList.contains('ready')) { done(); return; }
    var rects = flyRects(tile, { left: box.left, top: box.top, width: box.width, height: box.height });
    var fly = flyEl(m.currentSrc || m.src, rects.box);
    tile.classList.add('flying');
    m.style.visibility = 'hidden';
    var anim = fly.animate([rects.to, rects.from], { duration: 360, easing: 'cubic-bezier(0.3, 0, 0.1, 1)', fill: 'forwards' });
    anim.onfinish = anim.oncancel = function () {
      tile.classList.remove('flying');
      fly.remove();
      done();
    };
  }

  // ---- Open / close ----
  function open(items, start, tile, autoplay) {
    if (!items.length || isOpen) return;
    list = items;
    idx = Math.max(0, Math.min(start, list.length - 1));
    isOpen = true;
    closing = false;
    dx = 0;
    zoom = { z: 1, x: 0, y: 0 };
    ambient.reset();
    hideStrip();
    strip.innerHTML = '';
    dlg.querySelector('.ss-bg').style.opacity = '';
    root.classList.add('ss-lock');
    if (grid) grid.dropPreviews();
    dlg.showModal();
    measure();
    mountWindow();
    position(0);
    updateInfo();
    try { history.pushState({ gowsSlideshow: true }, ''); } catch (e) { }
    requestAnimationFrame(function () { dlg.classList.add('open'); });
    flyIn(tile);
    showChrome();
    setAuto(!!autoplay);
    playCurrentVideo();
    dlg.focus();
  }

  function requestClose() {
    if (!isOpen || closing) return;
    if (history.state && history.state.gowsSlideshow) history.back(); else close();
  }

  function close() {
    if (!isOpen || closing) return;
    closing = true;
    setAuto(false);
    accent.clear('ss');
    var m = currentMedia();
    // Captured before any reset so a swipe-down or zoomed image flies
    // back from where it is on screen.
    var box = m && m.getBoundingClientRect();
    if (m && m.tagName === 'VIDEO') m.pause();
    exitFullscreen();
    dlg.querySelector('.ss-bg').style.opacity = '0';
    dlg.classList.remove('open');
    flyOut(m, box, function () {
      resetZoom(false);
      dx = 0;
      setTimeout(function () {
        slides.forEach(unmountSlide);
        slides.clear();
        Array.prototype.slice.call(strip.querySelectorAll('img')).forEach(thumbLoader.cancel);
        strip.innerHTML = '';
        dlg.close();
        root.classList.remove('ss-lock');
        isOpen = false;
        closing = false;
      }, reducedMotion.matches ? 0 : 200);
    });
  }

  addEventListener('popstate', function () { if (isOpen) close(); });
  dlg.addEventListener('cancel', function (e) { e.preventDefault(); requestClose(); });

  $('ss-close').addEventListener('click', requestClose);
  btnPrev.addEventListener('click', function () { step(-1); });
  btnNext.addEventListener('click', function () { step(1); });
  btnAuto.addEventListener('click', function () { setAuto(!auto); });
  btnFull.addEventListener('click', toggleFullscreen);

  dlg.addEventListener('keydown', function (e) {
    if (!isOpen) return;
    if (e.target.tagName === 'VIDEO' && (e.key === ' ' || e.key.indexOf('Arrow') === 0)) return;
    if (e.key === ' ' && e.target !== dlg) return;
    showChrome();
    switch (e.key) {
      case 'ArrowRight': step(1); break;
      case 'ArrowLeft': step(-1); break;
      case 'Home': go(0); break;
      case 'End': go(list.length - 1); break;
      case ' ': setAuto(!auto); break;
      case 'f': if (fullscreenEnabled) btnFull.click(); break;
      default: return;
    }
    e.preventDefault();
  });

  function relayoutSlides() {
    measure();
    slides.forEach(function (div) {
      var p = insets();
      div.style.padding = p.t + 'px ' + p.r + 'px ' + p.b + 'px ' + p.l + 'px';
      if (div.firstChild) sizeMedia(div.firstChild);
    });
    resetZoom(false);
    position(0);
  }
  addEventListener('resize', function () { if (isOpen) { relayoutSlides(); renderStrip(); } });

  return { open: open };
})();
