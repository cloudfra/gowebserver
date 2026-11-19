var doc = document, root = doc.documentElement;
function $(id) { return doc.getElementById(id); }
function store(key, value) {
  try {
    if (value === null) localStorage.removeItem(key); else localStorage.setItem(key, value);
  } catch (e) { }
}
function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, function (c) {
    return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
  });
}
// A URL's path segment as a name to read: decoded, and an opened
// archive (NAME.d) named for the archive.
function decode(s) { try { return decodeURIComponent(s); } catch (e) { return s; } }
function label(s) { s = decode(s); return s.slice(-2) === '.d' ? s.slice(0, -2) : s; }
var SEP ='<svg class="sep" viewBox="0 0 24 24"><polyline points="9 6 15 12 9 18"/></svg>';

// ===== Theme =====
(function () {
  var btn = $('theme');
  var modes = ['auto', 'light', 'dark'];
  var labels = { auto: 'Theme: automatic', light: 'Theme: light', dark: 'Theme: dark' };
  var icons = {
    auto: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="8"/><path d="M12 4a8 8 0 010 16z" fill="currentColor"/></svg>',
    light: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>',
    dark: '<svg viewBox="0 0 24 24"><path d="M20 14.5A8 8 0 019.5 4a8 8 0 1010.5 10.5z"/></svg>'
  };
  function apply(mode) {
    if (mode === 'auto') delete root.dataset.theme; else root.dataset.theme = mode;
    btn.innerHTML = icons[mode];
    btn.setAttribute('aria-label', labels[mode]);
    btn.title = labels[mode];
  }
  var mode = root.dataset.theme || 'auto';
  apply(mode);
  btn.addEventListener('click', function () {
    mode = modes[(modes.indexOf(mode) + 1) % modes.length];
    store('gows-theme', mode === 'auto' ? null : mode);
    apply(mode);
  });
})();

// ===== Header: breadcrumbs, title, sticky toolbar =====
(function () {
  var parts = location.pathname.split('/').filter(Boolean);
  var crumbs = $('crumbs');
  // Parent folders shown after home, by screen width: 4 on wide
  // screens, 3 on medium, 2 on phones. Deeper ones collapse into an
  // ellipsis that links to the nearest hidden folder and shows the
  // full path on hover.
  var wide = matchMedia('(min-width: 1100px)'), medium = matchMedia('(min-width: 700px)');
  function renderCrumbs() {
    var limit = wide.matches ? 4 : medium.matches ? 3 : 2;
    var ancestors = [], href = '/';
    parts.slice(0, -1).forEach(function (p) {
      href += p + '/';
      ancestors.push({ href: href, name: label(p) });
    });
    var html = '<a href="/">' +
      '<svg viewBox="0 0 24 24"><path d="M4 11l8-7 8 7v8a1 1 0 01-1 1h-4v-6H9v6H5a1 1 0 01-1-1z"/></svg>' +
      '<span>' + escapeHTML(location.host || 'Home') + '</span></a>';
    var hidden = ancestors.slice(0, Math.max(0, ancestors.length - limit));
    if (hidden.length) {
      var path = hidden.map(function (a) { return a.name; }).join(' / ');
      html += SEP + '<a class="more" href="' + escapeHTML(hidden[hidden.length - 1].href) +
        '" title="' + escapeHTML(path) + '" aria-label="' + escapeHTML('Up to ' + hidden[hidden.length - 1].name) + '">\u2026</a>';
    }
    ancestors.slice(hidden.length).forEach(function (a) {
      html += SEP + '<a href="' + escapeHTML(a.href) + '">' + escapeHTML(a.name) + '</a>';
    });
    // A panel can't stay on a crumb that's about to be replaced.
    hoverPanel.close();
    crumbs.innerHTML = html;
  }
  renderCrumbs();
  [wide, medium].forEach(function (mq) {
    if (mq.addEventListener) mq.addEventListener('change', renderCrumbs);
  });

  // A folder's name comes from the URL the browser asked for, which
  // includes the serve path the server doesn't see.
  if ($('title').hasAttribute('data-from-path')) {
    var name = parts.length ? label(parts[parts.length - 1]) : (location.host || '/');
    $('title').textContent = name;
    $('tb-title').textContent = name;
    doc.title = name;
  }

  // The page title travels into the toolbar's title as it scrolls
  // away, so there's only ever one title on screen. Progress p runs
  // from 0, when the title reaches the top of the window, to 1, when
  // the toolbar sticks. Along the way the title is moved and scaled
  // (against the scroll) from its own place onto the toolbar title's,
  // drawn above the toolbar, while the toolbar's glass fades in with
  // --title-p. At 1 it sits exactly on the toolbar's title, and the
  // two swap. Positions come from layout offsets, which the title's
  // own transform doesn't change.
  var hero = $('hero'), title = $('title'), toolbar = $('toolbar'), tbTitle = $('tb-title');
  var start = 0, end = 1, lastP = -1, lastS = -1, queued = false;
  var geo = null;
  function docOffset(el) {
    var x = 0, y = 0;
    for (; el; el = el.offsetParent) { x += el.offsetLeft; y += el.offsetTop; }
    return { x: x, y: y };
  }
  function measure() {
    var t = docOffset(title), tb = docOffset(tbTitle), bar = docOffset(toolbar);
    var cs = getComputedStyle(title), tcs = getComputedStyle(tbTitle);
    var fs = parseFloat(cs.fontSize), line = parseFloat(cs.lineHeight) || fs * 1.08;
    geo = {
      top: t.y,
      dx: tb.x - t.x,
      scale: parseFloat(tcs.fontSize) / fs,
      // First line centers are matched: the toolbar's title center
      // within the stuck toolbar, and the page title's first line.
      tbCenter: (tb.y - bar.y) + tbTitle.offsetHeight / 2,
      lineCenter: line / 2
    };
    start = t.y - 8;
    end = Math.max(start + 1, docTop(hero) + hero.offsetHeight);
    lastP = lastS = -1;
    blend();
  }
  function docTop(el) {
    return docOffset(el).y;
  }
  function blend() {
    queued = false;
    var s = scrollY;
    var p = Math.min(1, Math.max(0, (s - start) / (end - start)));
    if (p === lastP && (p === 0 || p === 1)) return;
    if (p === lastP && s === lastS) return;
    lastP = p;
    lastS = s;
    hero.style.setProperty('--title-p', p);
    toolbar.style.setProperty('--title-p', p);
    var docked = p >= 1;
    hero.classList.toggle('title-docked', docked);
    toolbar.classList.toggle('title-docked', docked);
    if (p <= 0 || docked) {
      title.style.transform = '';
      return;
    }
    // Where the title is drawn this frame, in the window: its own
    // place (which scrolls) blended toward the toolbar title's.
    var k = 1 + (geo.scale - 1) * p;
    var ownTop = geo.top - s;
    // The toolbar follows the header until it sticks at the top.
    var barTop = Math.max(0, end - s);
    var targetTop = barTop + geo.tbCenter - geo.lineCenter * geo.scale;
    var top = ownTop + (targetTop - ownTop) * p;
    title.style.transform = 'translate(' + (geo.dx * p).toFixed(2) + 'px,' + (top - ownTop).toFixed(2) + 'px) scale(' + k.toFixed(4) + ')';
  }
  addEventListener('scroll', function () {
    if (!queued) { queued = true; requestAnimationFrame(blend); }
  }, { passive: true });
  addEventListener('resize', measure, { passive: true });
  // Web fonts and the summary line can change the header's height
  // after load.
  if (window.ResizeObserver) new ResizeObserver(measure).observe(hero);
  measure();
})();
