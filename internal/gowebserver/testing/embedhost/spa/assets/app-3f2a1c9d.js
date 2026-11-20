// A stand-in for a single-page app built by a bundler: it routes on the
// client and asks the host's API for its data.
const app = document.getElementById('app');

function render() {
  const route = location.pathname;
  app.innerHTML = '<div class="card"><dl>' +
    '<dt>Route</dt><dd id="route"></dd>' +
    '<dt>Query</dt><dd id="query"></dd>' +
    '<dt>API</dt><dd id="api">&hellip;</dd>' +
    '</dl></div>';
  document.getElementById('route').textContent = route;
  document.getElementById('query').textContent = location.search || '(none)';
  fetch('/api/hello').then((r) => r.json()).then((j) => {
    document.getElementById('api').textContent = j.message;
    document.body.dataset.ready = 'true';
  });
}

document.addEventListener('click', (e) => {
  const a = e.target.closest('a[data-route]');
  if (!a) return;
  e.preventDefault();
  history.pushState(null, '', a.getAttribute('href'));
  render();
});
addEventListener('popstate', render);
render();
