// ===== Virtualized file list =====
// The folder's folders and files, one row each. A folder can hold tens of
// thousands, so the page is sent them as an array (CFG.files, see
// data.js) and only the rows near the screen are built; the space of the
// rest above and below them is padding. Every row is as tall as the
// first one built (each has a top border, the first's transparent), so
// where a row goes is its place times that height.
if ($('files-body')) fileList = createFileList($('files-body'));

function createFileList(body) {
  // Rows are built this many screens past the screen and kept until
  // they're KEEP screens away, so a scroll back and forth doesn't build
  // the same rows again. A list of up to WHOLE rows is built whole, so
  // that find in page and Tab reach every row of an ordinary folder.
  var BUILD = 0.75, KEEP = 1.5, WHOLE = 200;
  // A row's height, until one is measured (the stand-in browser of the
  // tests has no layout, so there it stays this).
  var ROW_H = 58;
  var rowH = 0, measured = false;
  var rows = [], first = 0; // rows built, for vis.files[first...]
  var frame = 0;

  function buildRow(f, i) {
    var row = doc.createElement('div');
    row.className = i ? 'row' : 'row first';
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
    return row;
  }

  // Hands rows just put in the page to the folder previews: a strip
  // measures the room its row has, which a row out of the page hasn't.
  function watch(added) {
    if (folderPreviews) added.forEach(function (row) { folderPreviews.watch(row, row._file); });
  }

  function dropRow(row) {
    if (folderPreviews) folderPreviews.unwatch(row, row._file);
    row.remove();
  }

  function clear() {
    rows.forEach(dropRow);
    rows = [];
    first = 0;
  }

  // The rows from..to (inclusive) of n near a screen that starts viewTop
  // into the list, with over screens to spare either side.
  function span(viewTop, over, n) {
    if (n <= WHOLE) return [0, n - 1];
    var h = rowH || ROW_H, vh = innerHeight;
    return [
      Math.max(0, Math.min(n - 1, Math.floor((viewTop - vh * over) / h))),
      Math.max(0, Math.min(n - 1, Math.floor((viewTop + vh * (1 + over)) / h)))
    ];
  }

  function render() {
    frame = 0;
    var n = vis.files.length;
    if (!n) { clear(); pad(0, 0); return; }
    var viewTop = scrollY - (body.getBoundingClientRect().top + scrollY);
    var build = span(viewTop, BUILD, n), keep = span(viewTop, KEEP, n);
    var last = first + rows.length - 1;
    var from = build[0], to = build[1];
    if (rows.length && first <= to && last >= from) {
      // What's built and still close enough stays.
      from = Math.min(from, Math.max(keep[0], first));
      to = Math.max(to, Math.min(keep[1], last));
    } else {
      clear();
      first = from;
      last = from - 1;
    }
    while (first < from) { dropRow(rows.shift()); first++; }
    while (last > to) { dropRow(rows.pop()); last--; }
    if (from < first) {
      var before = doc.createDocumentFragment(), add = [];
      for (var i = from; i < first; i++) { add.push(buildRow(vis.files[i], i)); before.appendChild(add[add.length - 1]); }
      body.insertBefore(before, rows[0] || null);
      rows = add.concat(rows);
      first = from;
      watch(add);
    }
    if (to > last) {
      var after = doc.createDocumentFragment(), added = [];
      for (var j = last + 1; j <= to; j++) { var r = buildRow(vis.files[j], j); rows.push(r); added.push(r); after.appendChild(r); }
      body.appendChild(after);
      watch(added);
    }
    pad(first, n - 1 - (first + rows.length - 1));
    if (!measured) measure();
  }

  // The space of the rows that aren't built.
  function pad(above, below) {
    var h = rowH || ROW_H;
    body.style.paddingTop = above * h + 'px';
    body.style.paddingBottom = below * h + 'px';
  }

  // Takes the row height from a built row; once the page is laid out it
  // stays until the window changes size.
  function measure() {
    var h = rows.length ? rows[0].getBoundingClientRect().height : 0;
    if (!(h > 0)) return;
    measured = true;
    if (Math.abs(h - (rowH || ROW_H)) < 0.01) { rowH = h; return; }
    rowH = h;
    render();
  }

  function schedule() {
    if (!frame) frame = requestAnimationFrame(render);
  }

  addEventListener('scroll', schedule, { passive: true });
  addEventListener('resize', function () { measured = false; schedule(); }, { passive: true });

  return {
    // Builds the list again, for a new filter.
    relayout: function () { clear(); render(); },
    render: render,
    // The rows built now, in order.
    rows: function () { return rows.slice(); }
  };
}
