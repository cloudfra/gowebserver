// ===== Folder previews =====
// A folder in the list shows a strip of its photos and videos beside its
// name, fading in toward the name, and what it holds in place of
// "Folder", so a library kept in many nested folders can be browsed
// without opening each one. The server picks them (the folder's URL with
// ?preview, see folderpreview.go); it's asked only for folders that come
// near the screen, a couple at a time, and the thumbnails go through the
// thumbnail queue as heavy ones, so a strip never holds up a folder's own
// photos. Archives listed as folders are left alone.
var folderPreviews = (function () {
  if (!CFG.thumbs || !window.IntersectionObserver || !window.fetch) return null;
  var LIMIT = 2, waiting = [], asking = 0;
  // The strip's thumbnails are this many CSS pixels square (.row-strip
  // img in the stylesheet).
  var THUMB = 40;

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

  // What the folder holds, in the size column's few characters, and in
  // full for its tooltip.
  function describe(p) {
    var full = [];
    if (p.photos) full.push(plural(p.photos, 'photo', 'photos'));
    if (p.videos) full.push(plural(p.videos, 'video', 'videos'));
    if (p.folders) full.push(plural(p.folders, 'folder', 'folders'));
    var media = p.photos + p.videos;
    var short = !media ? (p.folders ? plural(p.folders, 'folder', 'folders') : 'Empty') :
      !p.videos ? plural(p.photos, 'photo', 'photos') :
        !p.photos ? plural(p.videos, 'video', 'videos') :
          plural(media, 'item', 'items');
    return { short: short, full: full.join(', ') || 'Empty' };
  }

  function show(row, url, p) {
    var d = describe(p);
    var size = row.querySelector('.size');
    if (size) { size.textContent = d.short; size.title = d.full; }
    // A video's thumbnail needs the server to grab frames.
    var items = (p.items || []).filter(function (it) { return !it.video || CFG.videoThumbs; });
    var main = row.querySelector('.row-main'), meta = row.querySelector('.meta');
    if (!items.length || !main || !meta) return;
    var strip = doc.createElement('span');
    strip.className = 'row-strip';
    strip.setAttribute('aria-hidden', 'true');
    // Long enough to read as a filmstrip: it fades out toward the name.
    if (items.length >= 3) strip.classList.add('fade');
    items.forEach(function (it) {
      var item = { url: url + it.file.split('/').map(encodeURIComponent).join('/'), video: !!it.video };
      var img = doc.createElement('img');
      img.alt = '';
      img.decoding = 'async';
      img.draggable = false;
      strip.appendChild(img);
      thumbLoader.load(img, tileThumbURL(item, THUMB), function (ok) {
        if (ok) img.classList.add('ready'); else img.remove();
      }, false, true);
    });
    main.insertBefore(strip, meta);
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
  return { describe: describe };
})();
