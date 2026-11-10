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
var folderPreviews = (function () {
  if (!CFG.thumbs || !window.IntersectionObserver || !window.fetch) return null;
  var LIMIT = 2, waiting = [], asking = 0;
  // The strip's thumbnails are this many CSS pixels square (.row-strip
  // img in the stylesheet).
  var THUMB = 40, GAP = 4;

  // The folder a row opens, or '' for a row that isn't one.
  function folderURL(row) {
    var a = row.querySelector('.row-icon');
    var href = a && a.getAttribute('href');
    return href && href.charAt(href.length - 1) === '/' ? href : '';
  }

  function next() {
    while (asking < LIMIT && waiting.length) ask(waiting.shift());
  }

  function ask(row) {
    asking++;
    var url = folderURL(row);
    fetch(url + '?preview', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (p) { if (p) show(row, url, p); })
      .catch(function () { })
      .then(function () { asking--; next(); });
  }

  function show(row, url, p) {
    // A video's thumbnail needs the server to grab frames.
    var items = (p.items || []).filter(function (it) { return !it.video || CFG.videoThumbs; });
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
    items = items.slice(0, Math.floor((room + GAP) / (THUMB + GAP)) + 1);
    // Long enough to read as a filmstrip: it fades out toward the name.
    if (items.length >= 3) strip.classList.add('fade');
    var settled = 0;
    items.forEach(function (it) {
      var item = { url: url + it.file.split('/').map(encodeURIComponent).join('/'), video: !!it.video };
      var img = doc.createElement('img');
      img.alt = '';
      img.decoding = 'async';
      img.draggable = false;
      strip.appendChild(img);
      thumbLoader.load(img, tileThumbURL(item, THUMB), function (ok) {
        if (ok) img.classList.add('ready'); else img.remove();
        if (++settled === items.length) tint(row, strip);
      }, false, true);
    });
  }

  // Tints row with the most vivid color of its strip's thumbnails, the
  // nearest ones (by the size) counting most. Lightness is left to the
  // theme (--accent-l), as for the page's accent.
  function tint(row, strip) {
    var imgs = Array.prototype.slice.call(strip.querySelectorAll('img.ready'));
    if (!imgs.length || typeof vividColor !== 'function') return;
    vividColor(imgs.map(function (el, i) { return { el: el, w: 1 / (1 + i * 0.25) }; })).then(function (col) {
      if (!col || !row.isConnected) return;
      row.style.setProperty('--row-h', col.h.toFixed(1));
      row.style.setProperty('--row-c', col.c.toFixed(3));
      row.classList.add('row-accent');
    });
  }

  var seen = new IntersectionObserver(function (entries) {
    entries.forEach(function (e) {
      if (!e.isIntersecting) return;
      seen.unobserve(e.target);
      waiting.push(e.target);
    });
    next();
  }, { rootMargin: '300px 0px' });
  for (var i = 0; i < rowEls.length; i++) {
    if (rowDir[i] && folderURL(rowEls[i])) seen.observe(rowEls[i]);
  }
  return {};
})();
