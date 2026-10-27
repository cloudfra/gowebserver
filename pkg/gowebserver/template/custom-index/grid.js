// ===== Virtualized media grid =====
var grid = null;
var mediaEl = $('media');
if (mediaEl) grid = createGrid(mediaEl);

function createGrid(el) {
  var rows = [];          // layout: {y, h, type, items, label, ...}
  var mounted = new Map(); // row index -> element
  var width = 0;
  var fast = false, pinned = false, lastY = scrollY, lastT = performance.now(), velocity = 0;
  var idleTimer = 0, ambientTimer = 0, frame = 0, scrolledAt = -Infinity;
  var byDate = CFG.sort.indexOf('date') === 0;

  function groupLabel(it) {
    if (byDate) return monthFmt.format(new Date(it.t * 1000));
    if (CFG.sort.indexOf('size') === 0) return fmtBytes(it.size);
    var c = it.name.charAt(0).toUpperCase();
    return /[A-Z]/.test(c) ? c : '#';
  }

  function layout() {
    el.hidden = false;
    width = el.clientWidth;
    rows = [];
    var y = 0;
    var narrow = width < 600;
    var push = function (r) { r.y = y; y += r.h; rows.push(r); return rows.length - 1; };

    function section(kind, list, cols, gap, tileW, tileH) {
      push({ type: 'head', kind: kind, h: rows.length ? 92 : 56, n: list.length, label: kind === 'video' ? 'Videos' : 'Photos' });
      var group = null, groupRow = null, row = null;
      list.forEach(function (it) {
        var g = byDate && kind === 'photo' ? groupLabel(it) : null;
        if (g !== group) {
          group = g;
          row = null;
          groupRow = null;
          if (g) { groupRow = { type: 'group', h: 50, label: g, n: 0 }; push(groupRow); }
        }
        if (groupRow) groupRow.n++;
        if (!row || row.items.length === cols) {
          row = { type: 'tiles', kind: kind, items: [], h: tileH + gap, tileW: tileW, tileH: tileH, gap: gap };
          push(row);
        }
        it.row = rows.length - 1;
        row.items.push(it);
      });
    }

    if (vis.videos.length) {
      var vGap = narrow ? 6 : 12;
      var vCols = Math.max(1, Math.round((width + vGap) / (narrow ? 360 : 400)));
      var vW = (width - vGap * (vCols - 1)) / vCols;
      section('video', vis.videos, vCols, vGap, vW, Math.round(vW * 9 / 16));
    }
    if (vis.photos.length) {
      var pGap = narrow ? 2 : 4;
      var target = narrow ? 116 : width < 1100 ? 172 : 208;
      var pCols = Math.max(3, Math.round((width + pGap) / (target + pGap)));
      var pW = (width - pGap * (pCols - 1)) / pCols;
      section('photo', vis.photos, pCols, pGap, pW, pW);
    }
    rows.forEach(function (r) {
      if (!r.label) r.label = groupLabel(r.items[0]);
    });
    el.style.height = y + 'px';
    el.hidden = y === 0;
  }

  // Index of the first row whose bottom is below y.
  function rowAt(y) {
    var lo = 0, hi = rows.length - 1;
    while (lo < hi) {
      var mid = (lo + hi) >> 1;
      if (rows[mid].y + rows[mid].h <= y) lo = mid + 1; else hi = mid;
    }
    return lo;
  }

  function top() {
    return el.getBoundingClientRect().top + scrollY;
  }

  function buildRow(r, i) {
    var div = doc.createElement('div');
    div.className = 'm-row';
    div.style.height = r.h + 'px';
    div.style.transform = 'translateY(' + r.y + 'px)';
    if (r.type === 'head') {
      div.classList.add('m-head');
      div.innerHTML = '<h2>' + r.label + '</h2><span class="m-count">' + numFmt.format(r.n) + '</span>' +
        '<button class="pill" type="button" data-play="' + r.kind + '">' + svgUse('ui-play') + 'Slideshow</button>';
    } else if (r.type === 'group') {
      div.classList.add('m-group');
      div.innerHTML = '<span>' + escapeHTML(r.label) + '</span><span class="m-count">' + numFmt.format(r.n) + '</span>';
    } else {
      r.items.forEach(function (it, j) {
        var a = doc.createElement('a');
        a.className = it.video ? 'tile vid' : 'tile';
        a.href = it.url;
        a.setAttribute('aria-label', it.name);
        a.style.width = r.tileW + 'px';
        a.style.height = r.tileH + 'px';
        a.style.left = (j * (r.tileW + r.gap)) + 'px';
        a._item = it;
        a._plain = it.video && !CFG.videoThumbs;
        // A video's picture is the server's thumbnail of it when it
        // makes them; the video itself comes on hover (previewVideo).
        a.innerHTML = (!it.video ? '<img alt="" decoding="async" draggable="false">' :
          CFG.videoThumbs ? '<img class="vthumb" alt="" decoding="async" draggable="false">' : '') +
          '<span class="cap">' + escapeHTML(it.name) + '</span>' +
          (it.video ? '<span class="badge">' + svgUse('ui-play') + '<span class="dur">' + (it.dur ? fmtDuration(it.dur) : '') + '</span></span>' : '');
        div.appendChild(a);
      });
    }
    div._row = i;
    return div;
  }

  function unmountRow(div) {
    div.querySelectorAll('img').forEach(thumbLoader.cancel);
    div.querySelectorAll('.vid').forEach(unloadVideo);
    div.remove();
  }

  // Loads tile a's picture; seen is whether the tile is on screen. A
  // thumbnail loads a little ahead of the screen. A tile that has to
  // be the video itself (a._plain) waits until it's actually seen,
  // and gives up its turn if it's scrolled off before then: a video
  // is too much to fetch and decode on the chance it's looked at.
  function loadTile(a, seen) {
    var it = a._item;
    if (a._plain) {
      if (seen && !a._loading) { a._loading = true; queueVideo(a); }
      else if (!seen && unqueueVideo(a)) a._loading = false;
      return;
    }
    if (a._loading) return;
    a._loading = true;
    var img = a.firstChild;
    thumbLoader.load(img, tileThumbURL(it, parseFloat(a.style.width)), function (ok) {
      if (ok || !it.video) { showTile(a, ok ? 'ready' : 'err'); return; }
      // The server couldn't grab a frame of this video: the tile is
      // the video itself, as when the server makes no thumbnails.
      img.remove();
      a._plain = true;
      a._loading = false;
      schedule();
    });
  }

  // Takes a tile whose picture is still waiting to load out of the
  // queue, so what's on screen now isn't behind what was scrolled
  // past. It queues again when it comes back.
  function unqueueTile(a) {
    if (a._loading && (a._plain ? unqueueVideo(a) : thumbLoader.unqueue(a.firstChild))) a._loading = false;
  }

  function render() {
    frame = 0;
    if (!rows.length) return;
    var vh = innerHeight;
    var viewTop = scrollY - top();
    // Rows are built this far past the screen, less far during a
    // fling, and kept until they're the full distance away either way:
    // dropping the ones a fling wouldn't build would throw away their
    // pictures, loaded or on the way, only to build and fetch them
    // again each time the scroll slowed.
    var keep = vh * 1.25, over = fast ? vh * 0.5 : keep;
    var first = rowAt(Math.max(0, viewTop - over));
    var last = rowAt(viewTop + vh + over);
    var keepFirst = rowAt(Math.max(0, viewTop - keep)), keepLast = rowAt(viewTop + vh + keep);
    mounted.forEach(function (div, i) {
      if (i < keepFirst || i > keepLast) { unmountRow(div); mounted.delete(i); }
    });
    var frag = null;
    for (var i = first; i <= last; i++) {
      if (!mounted.has(i)) {
        var div = buildRow(rows[i], i);
        mounted.set(i, div);
        (frag = frag || doc.createDocumentFragment()).appendChild(div);
      }
    }
    if (frag) el.appendChild(frag);
    // During a fling, tiles are laid out but nothing is fetched; the
    // images load once the scroll slows down.
    var loadTop = viewTop - vh * 0.5, loadBottom = viewTop + vh * 1.5;
    mounted.forEach(function (div, i) {
      var r = rows[i];
      if (r.type !== 'tiles') return;
      var near = !fast && r.y + r.h > loadTop && r.y < loadBottom;
      var seen = near && r.y + r.h > viewTop && r.y < viewTop + vh;
      for (var c = div.firstChild; c; c = c.nextSibling) {
        if (near) loadTile(c, seen); else unqueueTile(c);
      }
    });
  }

  // What follows the scroll runs here, once a frame, with every layout
  // read (the scrubber's, then the grid's) ahead of every write, so
  // nothing forces the page to be laid out mid-frame.
  function tick() {
    var s = scrubber.measure();
    render();
    scrubber.place(s);
  }

  function schedule() {
    if (!frame) frame = requestAnimationFrame(tick);
  }

  function onScroll() {
    var now = performance.now();
    var dt = Math.max(1, now - lastT);
    velocity = velocity * 0.5 + (Math.abs(scrollY - lastY) / dt) * 0.5;
    lastY = scrollY; lastT = scrolledAt = now;
    fast = pinned || (fast ? velocity > 1.2 : velocity > 2.5);
    schedule();
    clearTimeout(idleTimer);
    idleTimer = setTimeout(function () { fast = pinned; velocity = 0; schedule(); }, 110);
    clearTimeout(ambientTimer);
    ambientTimer = setTimeout(updateAmbient, 260);
  }

  // The backdrop shows the loaded photo closest to the middle of the
  // viewport. The accent comes from every loaded photo on screen, with
  // the backdrop photo counted double, so a vibrant photo anywhere in
  // view wins over a muted one in the middle.
  function updateAmbient() {
    var mid = innerHeight / 2, best = null, bestD = Infinity, onScreen = [];
    mounted.forEach(function (div) {
      if (div.classList.contains('m-head') || div.classList.contains('m-group')) return;
      var rect = div.getBoundingClientRect();
      if (rect.bottom <= 0 || rect.top >= innerHeight) return;
      // A photo, a video's thumbnail, or the video where it's the tile.
      var media = [];
      for (var c = div.firstChild; c; c = c.nextSibling) {
        var m = c.classList.contains('ready') && (c.querySelector('img') || c.querySelector('video'));
        if (m) media.push(m);
      }
      Array.prototype.push.apply(onScreen, media);
      var d = Math.abs(rect.top + rect.height / 2 - mid);
      if (d < bestD && media.length) { best = media[0]; bestD = d; }
    });
    if (!best) return;
    pageAmbient.show(best);
    accent.from('page', onScreen.map(function (el) { return { el: el, w: el === best ? 2 : 1 }; }));
  }

  function relayout(keepAnchor) {
    var anchor = null, anchorOffset = 0;
    if (keepAnchor && rows.length) {
      var viewTop = scrollY - top();
      var r = rows[rowAt(Math.max(0, viewTop))];
      if (r && r.items) { anchor = r.items[0]; anchorOffset = viewTop - r.y; }
    }
    mounted.forEach(unmountRow);
    mounted.clear();
    layout();
    if (anchor && anchor.row >= 0 && rows[anchor.row]) {
      scrollTo(0, top() + rows[anchor.row].y + anchorOffset);
    }
    render();
    scrubber.refresh();
  }

  // Makes sure the tile for an item is in the DOM and on screen,
  // scrolling the page if needed; returns the tile element.
  function reveal(it) {
    var r = rows[it.row];
    if (!r) return null;
    var t = top(), vh = innerHeight;
    var tb = parseFloat(getComputedStyle(root).getPropertyValue('--toolbar-h')) || 60;
    var y = t + r.y;
    if (y < scrollY + tb || y + r.tileH > scrollY + vh) {
      scrollTo(0, Math.max(0, y - (vh - r.tileH) / 2));
      lastY = scrollY;
      render();
    }
    return tileFor(it);
  }

  function tileFor(it) {
    var div = mounted.get(it.row);
    if (!div) return null;
    for (var c = div.firstChild; c; c = c.nextSibling) if (c._item === it) return c;
    return null;
  }

  // Label shown next to the scrubber for a document scroll position.
  function labelAt(docY) {
    var y = docY - top();
    if (y < 0 || !rows.length) return '';
    var r = rows[rowAt(y)];
    return r ? r.label : '';
  }

  el.addEventListener('click', function (e) {
    var play = e.target.closest('[data-play]');
    if (play) {
      var list = play.dataset.play === 'video' ? vis.videos : vis.photos;
      slideshow.open(list, 0, null, true);
      return;
    }
    var a = e.target.closest('.tile');
    if (!a || e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    var it = a._item;
    slideshow.open(it.video ? vis.videos : vis.photos, it.pos, a, false);
  });

  // Muted preview on hover: a mouse or trackpad only, never a finger,
  // even on a tablet that has both. Only once the pointer stays on the
  // tile (HOVER_INTENT), not as it crosses tiles on its way somewhere:
  // each preview is a video to fetch and decode.
  el.addEventListener('pointerover', function (e) {
    if (e.pointerType === 'touch' || !finePointer.matches) return;
    var a = e.target.closest('.tile.vid');
    if (!a || a._intent || a._hovering) return;
    a._intent = setTimeout(function () {
      a._intent = 0;
      previewVideo(a, true);
    }, HOVER_INTENT);
  });
  el.addEventListener('pointerout', function (e) {
    var a = e.target.closest('.tile.vid');
    if (!a || a.contains(e.relatedTarget)) return;
    clearTimeout(a._intent);
    a._intent = 0;
    previewVideo(a, false);
  });

  var lastWidth = 0;
  new ResizeObserver(function () {
    if (el.clientWidth !== lastWidth) { lastWidth = el.clientWidth; relayout(true); }
  }).observe(el);
  addEventListener('scroll', onScroll, { passive: true });
  addEventListener('resize', schedule, { passive: true });

  return {
    relayout: relayout,
    render: render,
    reveal: reveal,
    labelAt: labelAt,
    setFast: function (v) { pinned = fast = v; if (!v) schedule(); },
    // Drops every hover preview, for the slideshow, whose videos come
    // first while it covers the grid.
    dropPreviews: function () {
      el.querySelectorAll('.tile.vid').forEach(function (a) {
        clearTimeout(a._intent);
        a._intent = 0;
      });
      previews.slice().forEach(unloadVideo);
    },
    // Whether the page is on the move: it scrolled within the last
    // few frames.
    scrolling: function () { return performance.now() - scrolledAt < SCROLL_SETTLE; }
  };
}

// How long after a scroll event the page still counts as scrolling.
var SCROLL_SETTLE = 150;

// Shows tile a's picture (cls is ready) or placeholder (err). A
// picture fades in, and quickly (.quick) while the page is scrolling:
// each fade is a layer of its own for the browser to blend on every
// frame, a screen of them at a time, just when it's busiest, so they
// are kept short. Not left out, though: pictures arrive a few at a
// time, and a screen filling in steps with no fade at all flickers.
// The tile gets its usual fade back once that one is over, for what
// else uses it (a video tile's thumbnail fades out and in around its
// preview).
var QUICK_FADE = 150, quickened = [], quickTimer = 0;
function showTile(a, cls) {
  if (grid && grid.scrolling()) {
    a.classList.add('quick');
    quickened.push(a);
    if (!quickTimer) quickTimer = setTimeout(unquicken, QUICK_FADE * 2);
  }
  a.classList.add(cls);
}

function unquicken() {
  quickTimer = 0;
  quickened.forEach(function (a) { a.classList.remove('quick'); });
  quickened = [];
}

// ===== Grid video tiles =====
// A video tile shows the server's thumbnail of the video, and the
// video itself exists only once the pointer has been over the tile,
// which is when it can play: a video element is expensive (a decoder,
// buffers, a connection), and a screen of them is a screen of videos
// being fetched and decoded for a picture each.
// When the server makes no thumbnails (or can't of this video), the
// tile has to be the video, paused on a frame. That one is made when
// the tile comes on screen, not ahead of it, at most a few loading at
// once.
// Either way the video is torn down when its row scrolls away.
var videoQueue = [], videoActive = 0, VIDEO_CONCURRENCY = 3;

function fmtDuration(sec) {
  var t = Math.floor(sec), h = Math.floor(t / 3600), m = Math.floor(t % 3600 / 60), s = t % 60;
  var pad = function (n) { return n < 10 ? '0' + n : '' + n; };
  return h ? h + ':' + pad(m) + ':' + pad(s) : m + ':' + pad(s);
}

function queueVideo(a) {
  videoQueue.push(a);
  pumpVideos();
}

// Takes tile a out of the queue; false when it wasn't waiting there.
function unqueueVideo(a) {
  var i = videoQueue.indexOf(a);
  if (i < 0) return false;
  videoQueue.splice(i, 1);
  return true;
}

function pumpVideos() {
  while (videoActive < VIDEO_CONCURRENCY && videoQueue.length) {
    var a = videoQueue.shift();
    if (a.isConnected) mountVideo(a);
  }
}

// A muted, looping video for tile a, which learns the video's length
// from it for its badge (the page isn't told it).
function tileVideo(a) {
  var it = a._item;
  var v = doc.createElement('video');
  v.muted = true;
  v.loop = true;
  v.playsInline = true;
  v.disablePictureInPicture = true;
  v.setAttribute('aria-hidden', 'true');
  v.addEventListener('loadedmetadata', function () {
    if (!isFinite(v.duration)) return;
    it.dur = v.duration;
    var d = a.querySelector('.dur');
    if (d) d.textContent = fmtDuration(v.duration);
  }, { once: true });
  v.addEventListener('playing', function () { if (a._hovering) revealVideo(a); });
  return v;
}

// Makes tile a its video, for want of a thumbnail: the fragment asks
// the browser for a frame from just after the start instead of a
// (often black) first frame.
function mountVideo(a) {
  var v = tileVideo(a);
  var done = false;
  videoActive++;
  var release = function () {
    if (done) return;
    done = true;
    videoActive--;
    pumpVideos();
  };
  a._release = release;
  v.preload = 'metadata';
  v.addEventListener('loadedmetadata', function () { showTile(a, 'ready'); release(); }, { once: true });
  v.addEventListener('error', function () { showTile(a, 'err'); release(); }, { once: true });
  setTimeout(release, 10000);
  v.src = a._item.url + '#t=0.5';
  a.insertBefore(v, a.firstChild);
}

// How long a video tile's thumbnail takes to fade back in, from the
// .tile img transition.
var VTHUMB_FADE_BACK = 400;

// How long the pointer stays on a video tile before its preview starts.
var HOVER_INTENT = 300;

// The tiles with a hover preview, least recently hovered first. Only
// the last WARM_PREVIEWS keep theirs once the pointer has left, so
// going back to one is quick; older ones are unloaded, rather than
// every tile the pointer has rested on holding a video (a decoder and
// buffers) until its row scrolls away.
var previews = [], WARM_PREVIEWS = 2;

function keepPreview(a) {
  var i = previews.indexOf(a);
  if (i >= 0) previews.splice(i, 1);
  previews.push(a);
  while (previews.length > WARM_PREVIEWS) {
    var old = previews[0];
    if (old._hovering) break;
    unloadVideo(old);
  }
}

// Starts (on) or stops the hover preview of video tile a. Under a
// thumbnail the video is made now, the first time, and the thumbnail
// fades out only once it is actually playing, so there's no flash of
// a blank frame; it fades back in on leave before the video pauses.
// Later hovers fade quickly, since the video is buffered by then.
function previewVideo(a, on) {
  clearTimeout(a._pauseTimer);
  a._hovering = on;
  var v = a.querySelector('video');
  if (!a.querySelector('.vthumb')) {
    if (!v) return;
    if (on) v.play().catch(function () { }); else v.pause();
    return;
  }
  if (!on) {
    a.classList.remove('vplay');
    if (v) a._pauseTimer = setTimeout(function () { v.pause(); }, VTHUMB_FADE_BACK);
    return;
  }
  if (!v) {
    // Not before the thumbnail shows: there's nothing to fade from.
    if (!a.classList.contains('ready')) return;
    v = tileVideo(a);
    v.src = a._item.url;
    // Ahead of the thumbnail, which is then drawn over it.
    a.insertBefore(v, a.firstChild);
  }
  keepPreview(a);
  // Back before the video paused: it's still playing, so show it now.
  if (!v.paused && v.readyState > 2) revealVideo(a);
  v.play().catch(function () { });
}

function revealVideo(a) {
  if (a._warm) a.classList.add('vwarm');
  a.classList.add('vplay');
  a._warm = true;
}

function unloadVideo(a) {
  clearTimeout(a._pauseTimer);
  var i = previews.indexOf(a);
  if (i >= 0) previews.splice(i, 1);
  a._hovering = a._warm = false;
  a.classList.remove('vplay', 'vwarm');
  var v = a.querySelector('video');
  if (v) {
    v.pause();
    v.removeAttribute('src');
    v.load();
    v.remove();
  }
  if (a._release) a._release();
}
