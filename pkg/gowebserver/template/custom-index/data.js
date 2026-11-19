function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, function (c) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
  });
}

// ===== Data =====
var all = CFG.media.map(function (m, i) {
  return { video: m[0] === 1, url: m[1], name: m[2], size: m[3], t: m[4], lower: m[2].toLowerCase(), pos: 0, row: -1 };
});
// The list's folders and files (kind: 0 a file, 1 one the text viewer
// shows, 2 a folder, 3 an archive). list.js builds rows only for those
// near the screen.
var listEntries = (CFG.files || []).map(function (f) {
  return { kind: f[0], dir: f[0] >= 2, url: f[1], name: f[2], icon: f[3], size: f[4], date: f[5], lower: f[2].toLowerCase() };
});
var vis = { files: [], photos: [], videos: [] };
var lastQuery = null;
// The file list (list.js), when the folder has one.
var fileList = null;

function summarize(folders, files) {
  var parts = [];
  if (folders) parts.push(plural(folders, 'folder', 'folders'));
  if (files) parts.push(plural(files, 'file', 'files'));
  if (vis.photos.length) parts.push(plural(vis.photos.length, 'photo', 'photos'));
  if (vis.videos.length) parts.push(plural(vis.videos.length, 'video', 'videos'));
  $('summary').textContent = parts.join(', ');
  $('no-match').hidden = !!(folders || files || vis.photos.length || vis.videos.length) || !listEntries.length && !all.length;
}

function applyFilter(q) {
  q = q.trim().toLowerCase();
  // The same filter again (a trailing space, say) changes nothing.
  if (q === lastQuery) return;
  lastQuery = q;
  var i, folders = 0, files = 0;
  vis.files = [];
  for (i = 0; i < listEntries.length; i++) {
    var f = listEntries[i];
    // Its place among what shows, or -1.
    f.pos = -1;
    if (q && f.lower.indexOf(q) < 0) continue;
    if (f.dir) folders++; else files++;
    f.pos = vis.files.length;
    vis.files.push(f);
  }
  vis.photos = []; vis.videos = [];
  for (i = 0; i < all.length; i++) {
    var it = all[i];
    if (q && it.lower.indexOf(q) < 0) continue;
    var list = it.video ? vis.videos : vis.photos;
    it.pos = list.length;
    list.push(it);
  }
  summarize(folders, files);
  if (fileList) fileList.relayout();
  if (grid) grid.relayout(false);
}
