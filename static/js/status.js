// Order tracking page: poll the status endpoint and update the page.
(function () {
  const card = document.getElementById('status');
  if (!card) return;
  const labelEl = document.getElementById('status-label');
  const hintEl = document.getElementById('status-hint');
  const order = ['new', 'preparing', 'ready'];

  function show(status, label) {
    card.dataset.status = status;
    labelEl.textContent = label;
    const idx = order.indexOf(status);
    card.querySelectorAll('.steps li').forEach(function (li) {
      const i = order.indexOf(li.dataset.step);
      li.classList.toggle('done', idx >= 0 && i <= idx);
    });
    if (status === 'ready') hintEl.textContent = 'Come and collect it. Thank you!';
    if (status === 'collected' || status === 'cancelled') hintEl.textContent = '';
  }

  function poll() {
    fetch('/order/' + card.dataset.id + '/status.json', { cache: 'no-store' })
      .then(function (r) { return r.json(); })
      .then(function (d) { show(d.status, d.label); })
      .catch(function () { /* try again next tick */ });
  }

  show(card.dataset.status, labelEl.textContent);
  setInterval(poll, 5000);
})();
