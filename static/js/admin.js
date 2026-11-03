// Ask before deleting. Forms opt in with data-confirm="message".
document.addEventListener('submit', function (e) {
  const msg = e.target.dataset.confirm;
  if (msg && !window.confirm(msg)) e.preventDefault();
});
