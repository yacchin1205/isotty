// The viewer lets the human comment on what the agent showed them: a line
// or a range of a text, a block of a Markdown page, a spot or an area of
// an image, or the file as a whole. The comments go to the server beside
// the project, where the sync carries them into the VM.
(function () {
  'use strict';
  const kind = document.body.dataset.kind;
  const reviewsURL = document.body.dataset.reviews;
  const content = document.getElementById('content');
  const list = document.getElementById('list');
  const status = document.getElementById('status');
  let comments = [];
  let fileSHA = '';
  let selection = null; // the anchor being composed

  function say(text) {
    status.textContent = text;
  }

  async function call(method, body) {
    const res = await fetch(reviewsURL, {
      method,
      headers: body ? { 'content-type': 'application/json' } : {},
      body: body ? JSON.stringify(body) : undefined,
    });
    if (!res.ok) {
      let reason = res.statusText;
      try { reason = (await res.json()).error || reason; } catch (e) { /* not JSON */ }
      throw new Error(reason);
    }
    return res.json();
  }

  async function load() {
    try {
      const answer = await call('GET');
      comments = answer.comments;
      fileSHA = answer.file_sha256;
      render();
      say('');
    } catch (err) {
      say('comments could not be loaded: ' + err.message);
    }
  }

  function where(anchor) {
    if (anchor.lines) {
      return anchor.lines[0] === anchor.lines[1] ? 'line ' + anchor.lines[0] : 'lines ' + anchor.lines[0] + '–' + anchor.lines[1];
    }
    if (anchor.point) {
      return 'at (' + pct(anchor.point[0]) + ', ' + pct(anchor.point[1]) + ')';
    }
    if (anchor.rect) {
      return 'area at (' + pct(anchor.rect[0]) + ', ' + pct(anchor.rect[1]) + ') ' + pct(anchor.rect[2]) + ' × ' + pct(anchor.rect[3]);
    }
    return 'the whole file';
  }

  function pct(v) {
    return Math.round(v * 100) + '%';
  }

  // --- selection by kind ---

  function textQuote(from) {
    const row = document.getElementById('L' + from);
    return row ? row.querySelector('.c').textContent.trim().slice(0, 200) : '';
  }

  function selectLines(from, to) {
    selection = { lines: [from, to], quote: textQuote(from) };
    for (const row of content.querySelectorAll('tr.sel')) row.classList.remove('sel');
    for (let n = from; n <= to; n++) {
      const row = document.getElementById('L' + n);
      if (row) row.classList.add('sel');
    }
    compose();
  }

  if (kind === 'text') {
    let last = null;
    content.addEventListener('click', (event) => {
      const cell = event.target.closest('td.n');
      if (!cell) return;
      const n = Number(cell.dataset.line);
      if (event.shiftKey && last !== null) {
        selectLines(Math.min(last, n), Math.max(last, n));
      } else {
        last = n;
        selectLines(n, n);
      }
    });
  }

  if (kind === 'markdown') {
    content.addEventListener('click', (event) => {
      if (event.target.closest('a')) return;
      const block = event.target.closest('[data-lines]');
      if (!block) return;
      const [from, to] = block.dataset.lines.split('-').map(Number);
      for (const el of content.querySelectorAll('[data-lines].sel')) el.classList.remove('sel');
      block.classList.add('sel');
      selection = { lines: [from, to], quote: block.textContent.trim().replace(/\s+/g, ' ').slice(0, 200) };
      compose();
    });
  }

  if (kind === 'image') {
    const canvas = content.querySelector('.canvas');
    const image = document.getElementById('image');
    let start = null;
    const fraction = (event) => {
      const box = image.getBoundingClientRect();
      return [clamp((event.clientX - box.left) / box.width), clamp((event.clientY - box.top) / box.height)];
    };
    canvas.addEventListener('mousedown', (event) => {
      if (event.button !== 0) return;
      start = fraction(event);
      event.preventDefault();
    });
    canvas.addEventListener('mouseup', (event) => {
      if (!start) return;
      const end = fraction(event);
      const w = Math.abs(end[0] - start[0]);
      const h = Math.abs(end[1] - start[1]);
      if (w < 0.01 && h < 0.01) {
        selection = { point: round(end) };
      } else {
        selection = { rect: round([Math.min(start[0], end[0]), Math.min(start[1], end[1]), w, h]) };
      }
      start = null;
      render();
      compose();
    });
  }

  if (kind === 'other') {
    selection = {};
    compose();
  }

  function clamp(v) {
    return Math.min(1, Math.max(0, v));
  }

  function round(values) {
    return values.map((v) => Math.round(v * 1000) / 1000);
  }

  // --- composing ---

  function compose() {
    let box = document.getElementById('composer');
    if (!box) {
      box = document.createElement('div');
      box.id = 'composer';
      box.innerHTML = '<div class="where"></div><textarea placeholder="Your comment"></textarea><div class="tools"><button class="save">Save</button><button class="cancel">Cancel</button></div>';
      list.parentNode.insertBefore(box, list);
      box.querySelector('.save').addEventListener('click', save);
      box.querySelector('.cancel').addEventListener('click', clearSelection);
      box.querySelector('textarea').addEventListener('keydown', (event) => {
        if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') save();
      });
    }
    box.querySelector('.where').textContent = 'Comment on ' + where(selection);
    box.querySelector('textarea').focus();
  }

  function clearSelection() {
    selection = kind === 'other' ? {} : null;
    const box = document.getElementById('composer');
    if (box && kind !== 'other') box.remove();
    if (box && kind === 'other') box.querySelector('textarea').value = '';
    for (const el of content.querySelectorAll('.sel')) el.classList.remove('sel');
    render();
  }

  async function save() {
    const box = document.getElementById('composer');
    const body = box.querySelector('textarea').value.trim();
    if (!body || !selection) return;
    try {
      await call('POST', { anchor: selection, body });
      box.querySelector('textarea').value = '';
      clearSelection();
      await load();
    } catch (err) {
      say('saving failed: ' + err.message);
    }
  }

  // --- rendering ---

  function render() {
    list.innerHTML = '';
    for (const el of content.querySelectorAll('.has')) el.classList.remove('has');
    for (const mark of content.querySelectorAll('.mark')) mark.remove();
    const canvas = content.querySelector('.canvas');
    for (const comment of comments) {
      const item = document.createElement('li');
      item.className = (comment.resolved ? 'resolved' : '') + (comment.file_sha256 !== fileSHA ? ' stale' : '');
      const whereEl = document.createElement('span');
      whereEl.className = 'where';
      whereEl.textContent = where(comment.anchor) + ' · ' + new Date(comment.at).toLocaleString();
      whereEl.addEventListener('click', () => reveal(comment.anchor));
      const bodyEl = document.createElement('div');
      bodyEl.className = 'body';
      bodyEl.textContent = comment.body;
      const tools = document.createElement('div');
      tools.className = 'tools';
      const resolved = document.createElement('label');
      const check = document.createElement('input');
      check.type = 'checkbox';
      check.checked = comment.resolved;
      check.addEventListener('change', () => change(comment.id, { resolved: check.checked }));
      resolved.append(check, ' resolved');
      const edit = document.createElement('button');
      edit.textContent = 'Edit';
      edit.addEventListener('click', () => {
        const text = prompt('Edit the comment', comment.body);
        if (text && text.trim()) change(comment.id, { body: text.trim() });
      });
      const del = document.createElement('button');
      del.textContent = 'Delete';
      del.addEventListener('click', () => {
        if (confirm('Delete this comment?')) change(comment.id, null);
      });
      tools.append(resolved, edit, del);
      item.append(whereEl, bodyEl, tools);
      list.append(item);
      mark(comment, canvas);
    }
    if (selection && (selection.point || selection.rect) && canvas) {
      place(canvas, selection, 'mark sel');
    }
    if (!comments.length) {
      const hint = document.createElement('p');
      hint.className = 'hint';
      hint.textContent = { text: 'Click a line number to comment on it; shift-click to extend the range.', markdown: 'Click a paragraph, heading or list item to comment on it.', image: 'Click a spot, or drag over an area, to comment on it.', other: 'Comment on the file as a whole.' }[kind];
      list.append(hint);
    }
  }

  function mark(comment, canvas) {
    const a = comment.anchor;
    if (a.lines) {
      for (let n = a.lines[0]; n <= a.lines[1]; n++) {
        const row = document.getElementById('L' + n);
        if (row) row.querySelector('.n').classList.add('has');
      }
      // The innermost blocks over the lines carry the mark, not the list
      // or quote that contains them as well.
      for (const block of blocksOver(a.lines)) block.classList.add('has');
    } else if ((a.point || a.rect) && canvas) {
      place(canvas, a, 'mark' + (comment.resolved ? ' resolved' : ''));
    }
  }

  function blocksOver(lines) {
    const over = (block) => {
      const [from, to] = block.dataset.lines.split('-').map(Number);
      return from <= lines[1] && to >= lines[0];
    };
    return Array.from(content.querySelectorAll('[data-lines]')).filter((block) =>
      over(block) && !Array.from(block.querySelectorAll('[data-lines]')).some(over));
  }

  function place(canvas, anchor, className) {
    const el = document.createElement('div');
    el.className = className;
    if (anchor.point) {
      el.classList.add('point');
      el.style.left = pct(anchor.point[0]);
      el.style.top = pct(anchor.point[1]);
    } else {
      el.style.left = anchor.rect[0] * 100 + '%';
      el.style.top = anchor.rect[1] * 100 + '%';
      el.style.width = anchor.rect[2] * 100 + '%';
      el.style.height = anchor.rect[3] * 100 + '%';
    }
    canvas.append(el);
  }

  function reveal(anchor) {
    for (const el of content.querySelectorAll('.focus')) el.classList.remove('focus');
    let target = null;
    if (anchor.lines) {
      target = document.getElementById('L' + anchor.lines[0]) || blocksOver(anchor.lines)[0] || null;
    } else if (anchor.point || anchor.rect) {
      target = content.querySelector('.canvas');
    }
    if (target) {
      target.classList.add('focus');
      target.scrollIntoView({ block: 'center', behavior: 'smooth' });
    }
  }

  async function change(id, patch) {
    try {
      await call(patch ? 'PATCH' : 'DELETE', Object.assign({ id }, patch || {}));
      await load();
    } catch (err) {
      say('the change failed: ' + err.message);
    }
  }

  load();
})();
