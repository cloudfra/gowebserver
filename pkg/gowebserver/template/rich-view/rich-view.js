var doc = document, root = doc.documentElement;
function $(id) { return doc.getElementById(id); }
function store(key, value) {
  try {
    if (value === null) localStorage.removeItem(key); else localStorage.setItem(key, value);
  } catch (e) { }
}
var select = $('code-theme');
if (select) select.addEventListener('change', function () { location.href = select.value + location.hash; });

var wrap = $('wrap');
if (wrap) {
  var syncWrap = function () { wrap.setAttribute('aria-pressed', String(root.classList.contains('wrap'))); };
  syncWrap();
  wrap.addEventListener('click', function () {
    var on = root.classList.toggle('wrap');
    store('gows-wrap', on ? '1' : null);
    syncWrap();
  });
}

var copy = $('copy'), code = $('code');
if (copy && code && navigator.clipboard && window.isSecureContext) {
  copy.hidden = false;
  copy.addEventListener('click', function () {
    var text = Array.prototype.map.call(code.querySelectorAll('.cl'), function (l) { return l.textContent; }).join('');
    navigator.clipboard.writeText(text).then(function () {
      copy.classList.add('done');
      copy.title = 'Copied';
      setTimeout(function () { copy.classList.remove('done'); copy.title = 'Copy contents'; }, 1500);
    }, function () { });
  });
}

// A log file is followed while it is written: the server sends the lines
// added since the page was rendered, already highlighted with the page's own
// classes and numbered on from the lines shown, so they join the code below.
var follow = $('follow');
if (follow && code && window.EventSource) {
  var followURL = code.getAttribute('data-follow-url');
  if (followURL) {
    follow.hidden = false;
    var followNote = $('follow-note'), followLines = $('meta-lines'), followSize = $('meta-size');
    var followBox = code.querySelector('code') || code;
    var followFrom = parseInt(code.getAttribute('data-follow-offset'), 10) || 0;
    var followLine = parseInt(code.getAttribute('data-follow-line'), 10) || 1;
    var source = null;

    // A frame says where its first line belongs: everything the page has
    // from there on is out of date, which is how a log that was truncated or
    // rotated starts over.
    var addFrame = function (chunk) {
      for (var keep = chunk.start - 1; followBox.children.length > keep;) {
        followBox.children[followBox.children.length - 1].remove();
      }
      var holder = doc.createElement('div');
      holder.innerHTML = chunk.lines || '';
      while (holder.firstChild) followBox.appendChild(holder.firstChild);
      var total = chunk.start + chunk.count - 1;
      if (followLines) followLines.textContent = total + (total === 1 ? ' line' : ' lines');
      if (followSize && chunk.size) followSize.textContent = chunk.size;
      // Follow along, unless the person reading has scrolled elsewhere.
      var bottom = doc.documentElement.scrollHeight;
      if (window.innerHeight + window.scrollY >= bottom - 8) window.scrollTo(0, bottom);
    };

    var stopFollow = function () {
      if (source) { source.close(); source = null; }
      follow.setAttribute('aria-pressed', 'false');
    };

    var startFollow = function () {
      if (source) return;
      follow.setAttribute('aria-pressed', 'true');
      if (followNote) followNote.textContent = '';
      source = new EventSource(followURL + (followURL.indexOf('?') < 0 ? '?' : '&') +
        'offset=' + followFrom + '&line=' + followLine);
      source.addEventListener('lines', function (e) {
        var chunk;
        try { chunk = JSON.parse(e.data); } catch (err) { return; }
        if (!chunk) return;
        // Where this stream left off, so a later look starts from here.
        var mark = (e.lastEventId || '').split(':');
        if (mark.length === 2) {
          followFrom = parseInt(mark[0], 10) || followFrom;
          followLine = parseInt(mark[1], 10) || followLine;
        }
        addFrame(chunk);
      });
      source.addEventListener('gone', function () {
        stopFollow();
        if (followNote) followNote.textContent = 'This log file is gone.';
      });
      source.onerror = function () {
        if (source && source.readyState === 2) {
          stopFollow();
          if (followNote) followNote.textContent = 'Following stopped.';
        }
      };
    };

    follow.addEventListener('click', function () {
      if (source) { stopFollow(); store('gows-follow', null); }
      else { startFollow(); store('gows-follow', '1'); }
    });
    if (window.localStorage && window.localStorage.getItem('gows-follow') === '1') startFollow();
  }
}
