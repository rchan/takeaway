// Order page: +/- buttons and a live total. The server recalculates the real
// total from its own prices, so this is only a convenience.
(function () {
  const form = document.getElementById('order-form');
  if (!form) return;
  const totalEl = document.getElementById('total');

  function update() {
    let cents = 0;
    form.querySelectorAll('.qty').forEach(function (box) {
      const input = box.querySelector('input');
      const qty = Math.max(0, Math.min(20, parseInt(input.value, 10) || 0));
      cents += qty * parseInt(box.dataset.price, 10);
      box.classList.toggle('active', qty > 0);
    });
    totalEl.textContent = '£' + (cents / 100).toFixed(2);
  }

  form.addEventListener('click', function (e) {
    const btn = e.target.closest('.step');
    if (!btn) return;
    const input = btn.parentElement.querySelector('input');
    const next = (parseInt(input.value, 10) || 0) + parseInt(btn.dataset.dir, 10);
    input.value = Math.max(0, Math.min(20, next));
    update();
  });
  form.addEventListener('input', update);
  update();
})();
