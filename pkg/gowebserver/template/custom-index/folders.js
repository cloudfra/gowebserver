// ===== Folder covers =====
// A folder in the list shows a photo or video from it in place of its
// icon, and what it holds in place of "Folder", so a library kept in
// many nested folders can be browsed without opening each one. The
// server picks the cover (the folder's URL with ?cover, see
// foldercover.go); it's asked only for folders that come near the
// screen, a couple at a time, and the cover's thumbnail goes through
// the thumbnail queue as a heavy one, so covers never hold up the
// folder's own photos. Archives listed as folders are left alone.
var folderCovers = (function () {
  if (!CFG.thumbs || !window.IntersectionObserver || !window.fetch) return null;
  var LIMIT = 2, waiting = [], asking = 0;

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
    fetch(url + '?cover', { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (c) { if (c) show(row, url, c); })
      .catch(function () { })
      .then(function () { asking--; next(); });
  }

  // What the folder holds, in the size column's few characters, and in
  // full for its tooltip.
  function describe(c) {
    var full = [];
    if (c.photos) full.push(plural(c.photos, 'photo', 'photos'));
    if (c.videos) full.push(plural(c.videos, 'video', 'videos'));
    if (c.folders) full.push(plural(c.folders, 'folder', 'folders'));
    var media = c.photos + c.videos;
    var short = !media ? (c.folders ? plural(c.folders, 'folder', 'folders') : 'Empty') :
      !c.videos ? plural(c.photos, 'photo', 'photos') :
        !c.photos ? plural(c.videos, 'video', 'videos') :
          plural(media, 'item', 'items');
    return { short: short, full: full.join(', ') || 'Empty' };
  }

  function show(row, url, c) {
    var d = describe(c);
    var size = row.querySelector('.size');
    if (size) { size.textContent = d.short; size.title = d.full; }
    if (!c.file || (c.video && !CFG.videoThumbs)) return;
    var chip = row.querySelector('.chip');
    if (!chip) return;
    var item = { url: url + c.file.split('/').map(encodeURIComponent).join('/'), video: !!c.video };
    var img = doc.createElement('img');
    img.alt = '';
    img.decoding = 'async';
    img.className = 'cover';
    chip.appendChild(img);
    // A tile as small as the chip asks for the smallest thumbnail.
    thumbLoader.load(img, tileThumbURL(item, chip.offsetWidth || 38), function (ok) {
      if (ok) chip.classList.add('has-cover'); else img.remove();
    }, false, true);
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
