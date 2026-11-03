// Kitchen board: poll the orders endpoint and redraw three columns.
(function () {
  const csrf = document.body.dataset.csrf;
  const conn = document.getElementById('conn');
  const cols = {};
  document.querySelectorAll('.col').forEach(function (c) { cols[c.dataset.col] = c; });
  let knownIDs = null; // null until the first load, so we do not beep for old orders

  // Which button moves an order forward, and where.
  const nextStep = {
    new: { to: 'preparing', text: 'Start cooking' },
    preparing: { to: 'ready', text: 'Mark ready' },
    ready: { to: 'collected', text: 'Collected' },
  };

  function el(tag, cls, text) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text; // textContent: never trust order text as HTML
    return e;
  }

  function age(iso) {
    const mins = Math.max(0, Math.round((Date.now() - new Date(iso)) / 60000));
    return mins < 1 ? 'just now' : mins + ' min';
  }

  function setStatus(id, status) {
    return fetch('/kitchen/orders/' + id + '/status', {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrf, 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'status=' + encodeURIComponent(status),
    }).then(refresh);
  }

  function card(o) {
    const c = el('article', 'ticket');
    const head = el('header');
    head.appendChild(el('strong', 'num', '#' + o.number));
    head.appendChild(el('span', 'who', o.name));
    head.appendChild(el('span', 'age', age(o.createdAt)));
    c.appendChild(head);

    const ul = el('ul');
    o.items.forEach(function (it) {
      const li = el('li');
      li.appendChild(el('b', null, it.qty + '×'));
      li.appendChild(document.createTextNode(' ' + it.name));
      ul.appendChild(li);
    });
    c.appendChild(ul);
    if (o.notes) c.appendChild(el('p', 'note', '📝 ' + o.notes));

    const actions = el('div', 'actions');
    const step = nextStep[o.status];
    if (step) {
      const b = el('button', 'btn', step.text);
      b.addEventListener('click', function () { b.disabled = true; setStatus(o.id, step.to); });
      actions.appendChild(b);
    }
    const x = el('button', 'btn ghost', 'Cancel');
    x.addEventListener('click', function () {
      if (window.confirm('Cancel order #' + o.number + '?')) setStatus(o.id, 'cancelled');
    });
    actions.appendChild(x);
    c.appendChild(actions);
    return c;
  }

  function beep() {
    try {
      const ctx = new (window.AudioContext || window.webkitAudioContext)();
      const osc = ctx.createOscillator();
      osc.frequency.value = 880;
      osc.connect(ctx.destination);
      osc.start();
      osc.stop(ctx.currentTime + 0.25);
    } catch (e) { /* browsers block audio until the page has been clicked */ }
  }

  function draw(orders) {
    Object.keys(cols).forEach(function (k) {
      cols[k].querySelector('.cards').replaceChildren();
    });
    const counts = { new: 0, preparing: 0, ready: 0 };
    orders.forEach(function (o) {
      if (!cols[o.status]) return;
      cols[o.status].querySelector('.cards').appendChild(card(o));
      counts[o.status]++;
    });
    Object.keys(cols).forEach(function (k) {
      cols[k].querySelector('.count').textContent = counts[k] ? '(' + counts[k] + ')' : '';
    });
    document.title = (counts.new ? '(' + counts.new + ') ' : '') + 'Kitchen';

    const ids = new Set(orders.map(function (o) { return o.id; }));
    if (knownIDs && orders.some(function (o) { return !knownIDs.has(o.id); })) beep();
    knownIDs = ids;
  }

  function refresh() {
    return fetch('/kitchen/orders.json', { headers: { Accept: 'application/json' }, cache: 'no-store' })
      .then(function (r) {
        if (r.status === 401) { window.location = '/kitchen/login'; return null; }
        return r.json();
      })
      .then(function (orders) {
        if (!orders) return;
        draw(orders);
        conn.textContent = 'Live · updated ' + new Date().toLocaleTimeString();
      })
      .catch(function () { conn.textContent = 'Connection lost, retrying…'; });
  }

  refresh();
  setInterval(refresh, 4000);
})();
