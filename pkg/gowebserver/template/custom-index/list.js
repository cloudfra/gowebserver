// ===== Virtualized file list =====
// The folder's folders and files, one row each. A folder can hold tens of
// thousands, so the page is sent them as an array (CFG.files, see
// data.js) and only the rows near the screen are built; the space of the
// rest above and below them is padding. Every row is as tall as the next
// (the stylesheet pins it), so where a row goes is its place times that
// height, which is measured from a built row.
if ($('files-body')) fileList = createFileList($('files-body'));

function createFileList(body) {
  // Rows are built this many screens past the screen and kept until
  // they're KEEP screens away, so a scroll back and forth doesn't build
  // the same rows again. A list of up to WHOLE rows is built whole, so
  // that find in page and Tab reach every row of an ordinary folder.
  var BUILD = 0.75, KEEP = 1.5, WHOLE = 200;
  // A row's height until one is measured, as the stylesheet pins it (the
  // stand-in browser of the tests has no layout, so there it stays this).
  var ROW_H = 57;
  var rowH = 0;
  var rows = [], first = 0; // rows built, for vis.files[first...]
  // A row with the keyboard focus that the screen has left: it stays in
  // the page, at its place (.parked), until the focus leaves it, so Tab
  // goes on from where it was.
  var parked = null;
  var frame = 0;

  function height() {
    return rowH || ROW_H;
  }

  function buildRow(f) {
    var row = doc.createElement('div');
    row.className = 'row';
    if (f.dir) row.setAttribute('data-dir', '');
    // A folder's link ends in a slash; an archive opens as the folder of
    // its content, and a text file in the text viewer.
    var open = f.url + (f.kind === 2 ? '/' : '');
    var main = f.kind === 1 ? f.url + '?view=rich' : f.kind === 3 ? f.url + '.d/' : open;
    var name = escapeHTML(f.name), icon = escapeHTML(f.icon);
    row.innerHTML = '<a class="row-icon" href="' + escapeHTML(open) + '" aria-label="Open ' + name + ' directly">' +
      '<span class="chip icon-' + icon + '">' + svgUse('icon-' + icon) + '</span></a>' +
      '<a class="row-main" href="' + escapeHTML(main) + '" title="' + name + '"><span class="name">' + name + '</span>' +
      '<span class="meta"><span class="size">' + (f.kind === 2 ? 'Folder' : fmtBytes(f.size)) + '</span>' +
      '<span class="date">' + escapeHTML(f.date) + '</span></span></a>';
    row._file = f;
    row._new = true;
    return row;
  }

  // The row for entry f, the one the page has if it has one.
  function rowFor(f, have) {
    var row = have && have.get(f);
    if (row) { have.delete(f); return row; }
    if (parked && parked._file === f) { row = unpark(); return row; }
    return buildRow(f);
  }

  // Hands rows just put in the page to the folder previews: a strip
  // measures the room its row has, which a row out of the page hasn't.
  function watch(list) {
    list.forEach(function (row) {
      row.classList.toggle('first', row._file.pos === 0);
      if (!row._new) return;
      row._new = false;
      if (folderPreviews) folderPreviews.watch(row, row._file);
    });
  }

  function dropRow(row) {
    if (row.contains(doc.activeElement) && row._file.pos >= 0) { park(row); return; }
    if (folderPreviews) folderPreviews.unwatch(row, row._file);
    row.remove();
  }

  function park(row) {
    if (parked && parked !== row) { var was = parked; parked = null; dropRow(was); }
    parked = row;
    row.classList.add('parked');
    place();
  }

  function unpark() {
    var row = parked;
    parked = null;
    row.classList.remove('parked');
    row.style.top = '';
    return row;
  }

  // Puts the parked row at its place in the list.
  function place() {
    if (parked) parked.style.top = parked._file.pos * height() + 'px';
  }

  // Changing what's built can take the focus from a row that's moved;
  // it's given back.
  function keepFocus(fn) {
    var active = doc.activeElement, had = active && active !== doc.body && body.contains(active);
    fn();
    if (had && active.isConnected && doc.activeElement !== active && active.focus) active.focus({ preventScroll: true });
  }

  // The rows from..to (inclusive) of n near a screen that starts viewTop
  // into the list, with over screens to spare either side.
  function span(viewTop, over, n) {
    if (n <= WHOLE) return [0, n - 1];
    var h = height(), vh = innerHeight;
    return [
      Math.max(0, Math.min(n - 1, Math.floor((viewTop - vh * over) / h))),
      Math.max(0, Math.min(n - 1, Math.floor((viewTop + vh * (1 + over)) / h)))
    ];
  }

  // Where the screen starts in the list, read with the height of a built
  // row, before anything is written, so the page is laid out once a
  // frame. A row whose height has changed (a zoom, the fonts) corrects
  // where every row goes.
  function look() {
    var top = body.getBoundingClientRect().top;
    var h = rows.length ? rows[0].getBoundingClientRect().height : 0;
    if (h > 0 && Math.abs(h - height()) > 0.01) rowH = h;
    return -top;
  }

  function render() {
    frame = 0;
    var n = vis.files.length;
    var viewTop = look();
    if (!n) { keepFocus(clear); pad(0, 0); return; }
    var build = span(viewTop, BUILD, n), keep = span(viewTop, KEEP, n);
    var last = first + rows.length - 1;
    var from = build[0], to = build[1];
    if (rows.length && first <= to && last >= from) {
      // What's built and still close enough stays.
      from = Math.min(from, Math.max(keep[0], first));
      to = Math.max(to, Math.min(keep[1], last));
    }
    keepFocus(function () {
      if (!rows.length || first > to || last < from) {
        clear();
        first = from;
        last = from - 1;
      }
      while (first < from) { dropRow(rows.shift()); first++; }
      while (last > to) { dropRow(rows.pop()); last--; }
      var add = [];
      if (from < first) {
        var before = doc.createDocumentFragment(), head = [];
        for (var i = from; i < first; i++) { var b = rowFor(vis.files[i]); head.push(b); before.appendChild(b); }
        body.insertBefore(before, rows[0] || null);
        rows = head.concat(rows);
        add = add.concat(head);
        first = from;
      }
      if (to > last) {
        var after = doc.createDocumentFragment();
        for (var j = last + 1; j <= to; j++) { var r = rowFor(vis.files[j]); rows.push(r); add.push(r); after.appendChild(r); }
        body.appendChild(after);
      }
      watch(add);
    });
    finish(n);
  }

  function clear() {
    rows.forEach(dropRow);
    rows = [];
    first = 0;
  }

  // Builds the list again for what now shows (a new filter), keeping the
  // rows of entries that still show where they are: a row isn't moved,
  // so its strip, its thumbnails, its slideshow and the pointer on it
  // stay, and only the rows that change are touched.
  function relayout() {
    var n = vis.files.length, viewTop = look();
    var have = new Map();
    rows.forEach(function (row) { have.set(row._file, row); });
    var range = n ? span(viewTop, BUILD, n) : [0, -1];
    var want = [];
    for (var i = range[0]; i <= range[1]; i++) want.push(rowFor(vis.files[i], have));
    keepFocus(function () {
      // What no longer shows, or no longer near the screen, goes; a
      // parked row too, once its entry doesn't show.
      have.forEach(dropRow);
      if (parked && parked._file.pos < 0) dropRow(unpark());
      // The rest are in order already: what's new goes in between.
      var cur = runAfter(null);
      want.forEach(function (row) {
        if (row === cur) { cur = runAfter(cur); return; }
        body.insertBefore(row, cur);
      });
      rows = want;
      first = range[0];
      watch(want);
    });
    finish(n);
  }

  // The built row in the page after node (the first, for null), past a
  // parked row.
  function runAfter(node) {
    var c = node ? node.nextSibling : body.firstChild;
    while (c && (c === parked || !c._file)) c = c.nextSibling;
    return c;
  }

  function finish(n) {
    pad(first, Math.max(0, n - 1 - (first + rows.length - 1)));
    place();
    // The first rows built give the height the rest are placed by.
    if (!rowH && rows.length && rows[0].getBoundingClientRect().height > 0) {
      rowH = rows[0].getBoundingClientRect().height;
      render();
    }
  }

  // The space of the rows that aren't built.
  function pad(above, below) {
    var h = height();
    body.style.paddingTop = above * h + 'px';
    body.style.paddingBottom = below * h + 'px';
  }

  function schedule() {
    if (!frame) frame = requestAnimationFrame(render);
  }

  // The focus left a parked row: it goes, as the rows around it did.
  // (Not when the window lost it: it's still the row's when it's back.)
  body.addEventListener('focusout', function () {
    setTimeout(function () {
      if (!parked || parked.contains(doc.activeElement) || (doc.hasFocus && !doc.hasFocus())) return;
      var row = unpark();
      if (folderPreviews) folderPreviews.unwatch(row, row._file);
      row.remove();
    }, 0);
  });
  addEventListener('scroll', schedule, { passive: true });
  addEventListener('resize', schedule, { passive: true });

  return {
    relayout: relayout,
    render: render,
    // The rows built now, in order.
    rows: function () { return rows.slice(); },
    // The row kept for the focus, if there is one.
    parked: function () { return parked; }
  };
}
