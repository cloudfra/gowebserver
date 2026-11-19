// ===== Folder previews =====
// A folder in the list shows a strip of its photos and videos between its
// name and its size, fading out toward the name, so a library kept in
// many nested folders can be browsed without opening each one. Once the
// strip is in, its most vivid color (vividColor, as the page takes its
// accent from its photos) tints the folder's row; a folder of gray or
// muted photos keeps the usual look. The server picks them (the folder's URL with
// ?preview, see folderpreview.go); it's asked only for folders that come
// near the screen, a couple at a time, and the thumbnails go through the
// thumbnail queue as heavy ones, so a strip never holds up a folder's own
// photos. Archives listed as folders are left alone.
// The list builds only the rows near the screen (list.js) and calls
// watch and unwatch as it builds and drops them; what a folder's preview
// is, and its color, are kept with the folder's entry, so a row built
// again shows its strip at once without asking the server again.
var folderPreviews = (function () {
  if (!CFG.thumbs || !window.IntersectionObserver || !window.fetch) return null;
  var LIMIT = 2, waiting = [], asking = 0;
  // The strip's thumbnails are this many CSS pixels square (.row-strip
  // img in the stylesheet).
  var THUMB = 40, GAP = 4;

  // A preview's item it, of the folder at url, as the grid has its items.
  function itemOf(url, it) {
    return { url: url + it.file.split('/').map(encodeURIComponent).join('/'), video: !!it.video };
  }

  function next() {
    while (asking < LIMIT && waiting.length) ask(waiting.shift());
  }

  // f.preview is the server's answer once there is one (false for none);
  // f.row is the row showing f, while there is one.
  function ask(f) {
    asking++;
    f.asked = true;
    fetch(f.url + '/?preview', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (p) {
        f.preview = p || false;
        if (p && f.row) show(f.row, f);
      })
      .catch(function () { f.preview = false; })
      .then(function () { asking--; next(); });
  }

  function show(row, f) {
    var url = f.url + '/';
    // A video's thumbnail needs the server to grab frames.
    var items = (f.preview.items || []).filter(function (it) { return !it.video || CFG.videoThumbs; });
    var main = row.querySelector('.row-main'), meta = row.querySelector('.meta');
    if (!items.length || !main || !meta) return;
    var strip = doc.createElement('span');
    strip.className = 'row-strip';
    strip.setAttribute('aria-hidden', 'true');
    // The strip takes the row's room between the name and the size; only
    // as many thumbnails as fit there are loaded, one more for the edge
    // it fades out at.
    row.classList.add('has-strip');
    main.insertBefore(strip, meta);
    var room = strip.clientWidth;
    if (room < THUMB) {
      // A long name leaves no room.
      strip.remove();
      row.classList.remove('has-strip');
      return;
    }
    // All of them are kept for the strip's slideshow (below).
    strip.all = items.map(function (it) { return itemOf(url, it); });
    items = items.slice(0, Math.floor((room + GAP) / (THUMB + GAP)) + 1);
    // Long enough to read as a filmstrip: it fades out toward the name.
    if (items.length >= 3) strip.classList.add('fade');
    // Its color is known from the last time the row was built.
    if (f.tint) paint(row, f.tint);
    var settled = 0;
    items.forEach(function (it, i) {
      var item = strip.all[i];
      var img = doc.createElement('img');
      img.alt = '';
      img.decoding = 'async';
      img.draggable = false;
      strip.appendChild(img);
      thumbLoader.load(img, tileThumbURL(item, THUMB), function (ok) {
        if (ok) img.classList.add('ready'); else img.remove();
        if (++settled === items.length && f.tint === undefined) tint(row, strip, f);
      }, false, true);
    });
  }

  // Tints row with the most vivid color of its strip's thumbnails, the
  // nearest ones (by the size) counting most. Lightness is left to the
  // theme (--accent-l), as for the page's accent.
  function tint(row, strip, f) {
    var imgs = Array.prototype.slice.call(strip.querySelectorAll('img.ready'));
    if (!imgs.length || typeof vividColor !== 'function') return;
    vividColor(imgs.map(function (el, i) { return { el: el, w: 1 / (1 + i * 0.25) }; })).then(function (col) {
      f.tint = col || null;
      if (col && f.row === row) paint(row, col);
    });
  }

  function paint(row, col) {
    row.style.setProperty('--row-h', col.h.toFixed(1));
    row.style.setProperty('--row-c', col.c.toFixed(3));
    row.classList.add('row-accent');
  }

  var seen = new IntersectionObserver(function (es) {
    es.forEach(function (e) {
      if (!e.isIntersecting) return;
      seen.unobserve(e.target);
      var f = e.target._file;
      if (f && waiting.indexOf(f) < 0) waiting.push(f);
    });
    next();
  }, { rootMargin: '300px 0px' });
  var slides = stripSlideshow();

  return {
    // The list built row for entry f.
    watch: function (row, f) {
      if (f.kind !== 2) return;
      f.row = row;
      row._file = f;
      if (f.preview) show(row, f);
      else if (!f.asked) seen.observe(row);
    },
    // The list is dropping row, which showed entry f: a filter or a jump
    // can drop it under the pointer, its slideshow playing.
    unwatch: function (row, f) {
      if (f.kind !== 2) return;
      if (slides && slides.isOpen(row)) slides.close();
      seen.unobserve(row);
      var i = waiting.indexOf(f);
      if (i >= 0) waiting.splice(i, 1);
      if (f.row === row) f.row = null;
      row.querySelectorAll('.row-strip img').forEach(thumbLoader.cancel);
    }
  };
})();

// ===== A strip's slideshow =====
// Resting the pointer on a folder row with a strip plays the folder's
// photos through the strip's own box: a panel fades in over it and the
// thumbnails step along it, one a moment, the folder's next one coming
// in at the size and the strip looping while the pointer stays. Nothing
// in it is a control and nothing in the list moves: it's the strip's
// size, inside the strip. It opens through the hover panels the
// breadcrumb uses too (page-theme/hover-panel.js), so it shares their
// dwell, pointer gate and closing rules, and only one of them is ever
// open. It plays only what the row's preview already sent (up to 20),
// at the strip's own thumbnail size, so nothing new is asked of the
// server beyond the next few thumbnails, which go through the
// thumbnail queue like the strip's. With reduced motion asked for, the
// strip stays as it is.
function stripSlideshow() {
  var hp = window.gowsHoverPanel;
  if (!hp) return;
  // How long each step shows, and the strip's thumbnails' size and gap
  // (as in folderPreviews).
  var STEP = 700, THUMB = 40, GAP = 4;
  var show = null;

  function stripOf(row) {
    return row.querySelector('.row-strip');
  }

  // Loads frame i of the track, if it isn't loading already.
  function want(track, i) {
    var img = track.children[i];
    if (!img || img._wanted) return;
    img._wanted = true;
    thumbLoader.load(img, tileThumbURL(img._item, THUMB), function (ok) {
      if (ok) img.classList.add('ready');
    }, false, true);
  }

  function play(row) {
    stop();
    var strip = stripOf(row), all = strip && strip.all;
    // The list (list.js) may have dropped the row, or built it again
    // without its strip yet, while the pointer rested on it.
    if (!row.isConnected || !all) return;
    // The folder's photos, then from the first again, round as often as
    // it takes to fill the strip once more past the last: wherever the
    // loop is, every slot has a photo, and its end looks like its start.
    var fit = Math.max(1, Math.floor((strip.clientWidth + GAP) / (THUMB + GAP)));
    var panel = doc.createElement('span');
    panel.className = 'strip-show hover-panel';
    panel.setAttribute('aria-hidden', 'true');
    var track = doc.createElement('span');
    track.className = 'strip-track';
    var frames = [];
    for (var f = 0; f < all.length + fit + 1; f++) frames.push(all[f % all.length]);
    frames.forEach(function (item) {
      var img = doc.createElement('img');
      img.alt = '';
      img.decoding = 'async';
      img.draggable = false;
      img._item = item;
      track.appendChild(img);
    });
    panel.appendChild(track);
    strip.appendChild(panel);
    for (var i = 0; i <= fit + 1; i++) want(track, i);
    var s = show = { row: row, panel: panel, track: track, k: 0, timer: 0 };
    function step() {
      if (show !== s) return;
      s.k++;
      track.classList.remove('jump');
      track.style.transform = 'translateX(' + s.k * (THUMB + GAP) + 'px)';
      want(track, s.k + fit + 1);
      if (s.k >= all.length) {
        // On the copy of the start: back to the start, unseen.
        s.timer = setTimeout(function () {
          if (show !== s) return;
          s.k = 0;
          track.classList.add('jump');
          track.style.transform = '';
          s.timer = setTimeout(step, STEP / 2);
        }, STEP / 2);
        return;
      }
      s.timer = setTimeout(step, STEP);
    }
    s.timer = setTimeout(step, STEP);
    // A frame after it's in the page, so the reveal transitions.
    requestAnimationFrame(function () { if (show === s) hp.show(panel, true); });
  }

  function stop() {
    if (!show) return;
    var s = show;
    show = null;
    clearTimeout(s.timer);
    hp.show(s.panel, false);
    // Once it has faded out: canceling takes a loaded picture away too.
    setTimeout(function () {
      Array.prototype.forEach.call(s.track.children, function (img) { if (img._wanted) thumbLoader.cancel(img); });
      s.panel.remove();
    }, 300);
  }

  // The list builds and drops rows as the page scrolls; anchors are
  // looked up as events come, so a row built again is one too.
  return hp.attach(doc.body, '.row.has-strip', {
    // Off the row, it closes at once: there's nothing to move into. Each
    // row waits out its own dwell (no move), so a sweep down the list
    // plays nothing.
    grace: 0,
    when: function (row) {
      var strip = stripOf(row);
      return !hp.reducedMotion.matches && !!strip && !!strip.all && strip.all.length > 1;
    },
    open: play,
    close: stop,
  });
}
