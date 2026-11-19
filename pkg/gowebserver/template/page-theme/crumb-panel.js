// ===== Breadcrumb folders =====
// Resting the pointer on a breadcrumb (or tabbing to it, or ArrowDown on
// it) drops a panel under it listing the folders beside it, the crumb's
// own marked, so moving to a sibling folder is one hover and one click
// rather than two page loads. The home crumb lists the top level, and
// the ellipsis the folders it stands for. Its folders come from the
// crumb's parent with ?folders (subfolders.go), asked when it opens, so
// they're as fresh as the folder; an answer is kept a few seconds, for
// a pointer going back and forth along the crumbs. Each is a plain link.
(function () {
  var crumbs = $('crumbs');
  if (!crumbs || !window.fetch) return;
  var KEEP = 5000, kept = {};
  var sort = (/[?&]sort=([\w-]+)/.exec(location.search || '') || [])[1];
  var FOLDER = '<svg viewBox="0 0 24 24"><path d="M3 7.5A2.5 2.5 0 015.5 5h3.6l2 2h7.4A2.5 2.5 0 0121 9.5v7a2.5 2.5 0 01-2.5 2.5h-13A2.5 2.5 0 013 16.5z"/></svg>';
  var ARCHIVE = '<svg viewBox="0 0 24 24"><rect x="3" y="4" width="18" height="5" rx="1.5"/><path d="M5 9v9.5A1.5 1.5 0 006.5 20h11a1.5 1.5 0 001.5-1.5V9M10 13h4"/></svg>';

  var panel = doc.createElement('nav');
  panel.id = 'crumb-panel';
  panel.className = 'hover-panel crumb-panel';
  doc.body.appendChild(panel);
  // Each opening gets a number; an answer for an older one is dropped.
  var shown = 0;

  // The folders in the folder at url, or null when they can't be had.
  function folders(url) {
    var now = performance.now(), k = kept[url];
    if (k && now - k.at < KEEP) return k.list;
    var list = fetch(url + '?folders' + (sort ? '&sort=' + sort : ''), { credentials: 'same-origin' })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (j) { return j && j.folders || null; })
      .catch(function () { return null; });
    kept[url] = { at: now, list: list };
    list.then(function (l) { if (!l) delete kept[url]; });
    return list;
  }

  // What a crumb's panel lists: {head, list} with list a promise of
  // [{name, href, archive, current}].
  function contents(a) {
    var href = a.getAttribute('href') || '/';
    if (a.classList.contains('more')) {
      // The folders the ellipsis stands for: every one above its link.
      var segs = href.split('/').filter(Boolean), up = '/', list = [];
      segs.forEach(function (s) {
        up += s + '/';
        list.push({ name: label(s), href: up });
      });
      return { head: 'Folders above', list: Promise.resolve(list) };
    }
    var parent = '/', name = '';
    if (href !== '/') {
      var m = /^(.*\/)([^/]+)\/$/.exec(href);
      if (!m) return null;
      parent = m[1];
      name = decode(m[2]);
    } else {
      // The top level: this page's own folder there is the one to mark.
      var first = location.pathname.split('/').filter(Boolean)[0];
      name = first ? decode(first) : '';
    }
    var head = parent === '/' ? (location.host || 'Home') : label(parent.split('/').filter(Boolean).pop());
    return {
      head: head,
      list: folders(parent).then(function (fs) {
        return fs && fs.map(function (f) {
          var seg = f.name + (f.archive ? '.d' : '');
          return { name: f.name, archive: f.archive, href: parent + encodeURIComponent(seg) + '/', current: seg === name };
        });
      }),
    };
  }

  function render(head, list) {
    var html = '<div class="cp-head">' + escapeHTML(head) + '</div><ul>';
    list.forEach(function (f) {
      html += '<li><a href="' + escapeHTML(f.href) + '"' + (f.current ? ' class="current" aria-current="true"' : '') + '>' +
        (f.archive ? ARCHIVE : FOLDER) + '<span>' + escapeHTML(f.name) + '</span></a></li>';
    });
    panel.innerHTML = html + '</ul>';
    panel.setAttribute('aria-label', head);
  }

  // Puts the panel under crumb a, its left edge on a's, kept inside the
  // window, as tall as the room below allows (it scrolls past that).
  function place(a) {
    var r = a.getBoundingClientRect(), gutter = 12;
    var room = Math.max(160, innerHeight - r.bottom - 6 - gutter);
    panel.style.maxHeight = room + 'px';
    var w = panel.offsetWidth;
    var left = Math.max(gutter, Math.min(r.left, innerWidth - w - gutter));
    panel.style.left = (left + scrollX) + 'px';
    panel.style.top = (r.bottom + 6 + scrollY) + 'px';
    var cur = panel.querySelector('.current');
    if (cur) panel.scrollTop = Math.max(0, cur.offsetTop - room / 2);
  }

  function links() {
    return panel.querySelectorAll('a');
  }

  var ctl = hoverPanel.attach(crumbs, 'a', {
    focus: true,
    panel: function () { return panel; },
    open: function (a, how) {
      var my = ++shown, what = contents(a);
      if (!what) { ctl.close(); return; }
      a.setAttribute('aria-controls', 'crumb-panel');
      a.setAttribute('aria-expanded', 'true');
      what.list.then(function (list) {
        if (my !== shown || !ctl.isOpen(a)) return;
        if (!list || !list.length) { ctl.close(); return; }
        render(what.head, list);
        place(a);
        hoverPanel.show(panel, true);
        if (how === 'key') (panel.querySelector('.current') || links()[0]).focus();
      });
    },
    close: function (a) {
      shown++;
      a.setAttribute('aria-expanded', 'false');
      hoverPanel.show(panel, false);
    },
  });

  // Up and down move along the folders; up from the first goes back to
  // the crumb. Tab moves through them as the links they are.
  panel.addEventListener('keydown', function (e) {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
    var as = links(), i = Array.prototype.indexOf.call(as, e.target);
    if (i < 0) return;
    e.preventDefault();
    e.stopPropagation();
    if (e.key === 'ArrowDown') { if (as[i + 1]) as[i + 1].focus(); }
    else if (i > 0) as[i - 1].focus();
    else {
      var anchor = crumbs.querySelector('.hp-anchor');
      if (anchor) anchor.focus();
    }
  });
})();
