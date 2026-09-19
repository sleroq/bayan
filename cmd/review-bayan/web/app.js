const storeKey = 'bayan-reviews-v1';
const reviews = JSON.parse(localStorage.getItem(storeKey) || '{}');
let cases = [], filtered = [], shown = 0;
const list = document.querySelector('#cases'), progress = document.querySelector('#progress');
const statusFilter = document.querySelector('#status'), kindFilter = document.querySelector('#kind');
const save = () => { localStorage.setItem(storeKey, JSON.stringify(reviews)); updateProgress(); };
const keyFor = c => `${c.source.id}:${c.botReplyId}:${c.currentId}:${c.originalId}`;
const reviewed = c => Boolean(reviews[keyFor(c)]?.classification);

function updateProgress() {
  const count = cases.filter(reviewed).length;
  progress.textContent = `${count} / ${cases.length} reviewed · ${filtered.length} shown`;
}
function mediaNode(item) {
  if (item.kind === 'video') {
    const video = document.createElement('video'); video.controls = true; video.preload = 'none'; video.src = item.url;
    if (item.poster) video.poster = item.poster;
    return video;
  }
  const img = document.createElement('img'); img.loading = 'lazy'; img.src = item.url; return img;
}
function side(label, item, link) {
  const figure = document.createElement('figure');
  const caption = document.createElement('figcaption');
  const anchor = document.createElement('a'); anchor.textContent = `${label} · ${item.kind} · ${item.path}`; anchor.href = link; anchor.target = '_blank'; anchor.rel = 'noreferrer';
  caption.append(anchor); figure.append(caption, mediaNode(item)); return figure;
}
function markActive(index) {
  list.querySelector('.active')?.classList.remove('active');
  list.querySelector(`[data-index="${index}"]`)?.classList.add('active');
}
function showIndex(index) {
  if (index < 0 || index >= filtered.length) return;
  while (shown <= index) renderBatch();
  const card = list.querySelector(`[data-index="${index}"]`);
  markActive(index);
  card?.scrollIntoView({block:'start'});
}
function visibleCard() {
  const cards = [...list.querySelectorAll('[data-index]')];
  const headerBottom = document.querySelector('header').getBoundingClientRect().bottom;
  const card = cards.reduce((closest, candidate) =>
    Math.abs(candidate.getBoundingClientRect().top - headerBottom) < Math.abs(closest.getBoundingClientRect().top - headerBottom) ? candidate : closest,
  cards[0]);
  if (card) markActive(Number(card.dataset.index));
  return card;
}
function choose(card, key, requested) {
  markActive(Number(card.dataset.index));
  const classification = reviews[key]?.classification === requested ? '' : requested;
  reviews[key] = {...reviews[key], classification};
  card.querySelectorAll('[data-class]').forEach(button => {
    button.classList.toggle('selected', button.dataset.class === classification);
  });
  save();
  if (!classification) return;
  if (classification === 'same-template') {
    card.querySelector('textarea').focus();
    return;
  }
  showIndex(Number(card.dataset.index) + 1);
}
function renderBatch() {
  const end = Math.min(shown + 24, filtered.length);
  for (; shown < end; shown++) {
    const c = filtered[shown], key = keyFor(c), review = reviews[key] || {};
    const card = document.querySelector('#card').content.firstElementChild.cloneNode(true);
    card.dataset.index = shown;
    card.querySelector('.meta').innerHTML = `<span>#${c.currentId} · ${escapeHTML(c.from || '')} · ${escapeHTML(c.date || '')}</span><span>reply #${c.botReplyId}</span>`;
    card.querySelector('.pair').append(side('Original', c.original, c.originalLink), side('Current', c.current, c.currentLink));
    card.querySelectorAll('[data-class]').forEach(button => {
      button.classList.toggle('selected', button.dataset.class === review.classification);
      button.onclick = () => choose(card, key, button.dataset.class);
    });
    const note = card.querySelector('textarea'); note.value = review.note || '';
    note.onchange = () => { reviews[key] = {...reviews[key], note: note.value}; save(); };
    note.onkeydown = event => {
      if (event.key === 'Escape') { note.blur(); return; }
      if (event.key !== 'Enter' || event.shiftKey) return;
      event.preventDefault();
      reviews[key] = {...reviews[key], note: note.value}; save(); note.blur();
      showIndex(Number(card.dataset.index) + 1);
    };
    card.onclick = event => { if (!event.target.closest('a,button,textarea')) markActive(Number(card.dataset.index)); };
    list.append(card);
  }
}
function applyFilters() {
  const status = statusFilter.value, kind = kindFilter.value;
  filtered = cases.filter(c => {
    if (status !== 'all' && (status === 'reviewed') !== reviewed(c)) return false;
    if (kind === 'mixed') return c.current.kind !== c.original.kind;
    return kind === 'all' || (c.current.kind === kind && c.original.kind === kind);
  });
  list.replaceChildren(); shown = 0; renderBatch(); markActive(0); updateProgress();
}
function selectedReviews() {
  return cases.filter(reviewed).map(c => ({source:c.source, botReplyId:c.botReplyId, currentId:c.currentId, originalId:c.originalId,
    classification:reviews[keyFor(c)].classification, note:reviews[keyFor(c)].note || '', botReplyLink:c.botReplyLink, currentLink:c.currentLink,
    originalLink:c.originalLink, currentMedia:c.current.path, originalMedia:c.original.path}));
}
document.querySelector('#export').onclick = () => {
  const blob = new Blob([JSON.stringify(selectedReviews(), null, 2)], {type:'application/json'}), a = document.createElement('a');
  a.href = URL.createObjectURL(blob); a.download = 'bayan-reviews.json'; a.click(); URL.revokeObjectURL(a.href);
};
document.querySelector('#copy').onclick = async () => {
  const lines = selectedReviews().map(r => `- ${r.classification}: bot reply ${r.botReplyLink || '#'+r.botReplyId}; current ${r.currentLink || '#'+r.currentId}; original ${r.originalLink || '#'+r.originalId}${r.note ? ` — ${r.note}` : ''}`);
  await navigator.clipboard.writeText(`Review these Bayan detections using the regression taxonomy:\n${lines.join('\n')}`);
};
statusFilter.onchange = kindFilter.onchange = applyFilters;
new IntersectionObserver(entries => { if (entries[0].isIntersecting) renderBatch(); }).observe(document.querySelector('#sentinel'));
document.addEventListener('keydown', event => {
  if (event.target.matches('textarea,input,select')) return;
  const card = visibleCard();
  if (!card) return;
  const index = Number(card.dataset.index), c = filtered[index], key = keyFor(c);
  const shortcut = event.key.toLowerCase();
  if (shortcut === 'g' || shortcut === '1') choose(card, key, 'duplicate');
  else if (shortcut === 'b' || shortcut === '2') choose(card, key, 'distinct');
  else if (shortcut === 'c' || shortcut === '3') choose(card, key, 'same-template');
  else if (shortcut === 'j' || event.key === 'ArrowDown') showIndex(index + 1);
  else if (shortcut === 'k' || event.key === 'ArrowUp') showIndex(index - 1);
  else if (event.key === 'Home') showIndex(0);
  else if (event.key === 'End') showIndex(filtered.length - 1);
  else return;
  event.preventDefault();
});
function escapeHTML(value) { const e = document.createElement('span'); e.textContent = value; return e.innerHTML; }
fetch('/api/cases').then(r => r.json()).then(data => { cases = data || []; applyFilters(); }).catch(err => { progress.textContent = `Failed: ${err}`; });
