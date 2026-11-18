// The viewer lets the human comment on what the agent showed them: a line
// or a range of a text, a block of a Markdown page, a spot or an area of
// an image, a moment of a video or a sound, a cell of a notebook, a line
// or a block of it or one of its outputs, or the file as a whole. The
// comments go to the server beside the project, where the sync carries
// them into the VM.
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

  function lineWords(lines) {
    return lines[0] === lines[1] ? 'line ' + lines[0] : 'lines ' + lines[0] + '–' + lines[1];
  }

  function where(anchor) {
    if (anchor.cell) {
      const cell = 'cell ' + anchor.cell;
      if (anchor.output) return cell + ', output ' + anchor.output;
      if (anchor.lines) return cell + ', ' + lineWords(anchor.lines);
      return cell;
    }
    if (anchor.lines) {
      return lineWords(anchor.lines);
    }
    if (anchor.point) {
      return 'at (' + pct(anchor.point[0]) + ', ' + pct(anchor.point[1]) + ')';
    }
    if (anchor.rect) {
      return 'area at (' + pct(anchor.rect[0]) + ', ' + pct(anchor.rect[1]) + ') ' + pct(anchor.rect[2]) + ' × ' + pct(anchor.rect[3]);
    }
    if (anchor.time !== undefined) {
      return 'at ' + clock(anchor.time);
    }
    return 'the whole file';
  }

  function clock(seconds) {
    const m = Math.floor(seconds / 60);
    const s = (seconds - m * 60).toFixed(1);
    return m + ':' + (s < 10 ? '0' : '') + s;
  }

  function pct(v) {
    return Math.round(v * 100) + '%';
  }

  // --- selection by kind ---

  // A notebook holds a text or a Markdown page per cell, and an anchor in
  // one names the cell; elsewhere the whole page is the one text.
  function scopeOf(anchor) {
    if (!anchor.cell) return content;
    return content.querySelector('section.cell[data-cell="' + anchor.cell + '"]');
  }

  function cellAnchor(section) {
    if (!section) return {};
    const anchor = { cell: Number(section.dataset.cell) };
    if (section.dataset.cellId) anchor.cell_id = section.dataset.cellId;
    return anchor;
  }

  function row(scope, n) {
    const number = scope && scope.querySelector('td.n[data-line="' + n + '"]');
    return number ? number.parentNode : null;
  }

  function unselect() {
    for (const el of content.querySelectorAll('.sel')) el.classList.remove('sel');
  }

  function selectLines(section, from, to) {
    const scope = section || content;
    const first = row(scope, from);
    selection = Object.assign(cellAnchor(section), { lines: [from, to], quote: first ? first.querySelector('.c').textContent.trim().slice(0, 200) : '' });
    unselect();
    for (let n = from; n <= to; n++) {
      const r = row(scope, n);
      if (r) r.classList.add('sel');
    }
    compose();
  }

  if (kind === 'text' || kind === 'notebook') {
    let last = null;
    let lastSection = null;
    content.addEventListener('click', (event) => {
      const number = event.target.closest('td.n');
      if (!number) return;
      const section = number.closest('section.cell');
      const n = Number(number.dataset.line);
      if (event.shiftKey && last !== null && section === lastSection) {
        selectLines(section, Math.min(last, n), Math.max(last, n));
      } else {
        last = n;
        lastSection = section;
        selectLines(section, n, n);
      }
    });
  }

  if (kind === 'markdown' || kind === 'notebook') {
    content.addEventListener('click', (event) => {
      if (event.target.closest('a') || event.target.closest('.output')) return;
      const block = event.target.closest('[data-lines]');
      if (!block) return;
      const [from, to] = block.dataset.lines.split('-').map(Number);
      unselect();
      block.classList.add('sel');
      selection = Object.assign(cellAnchor(block.closest('section.cell')), { lines: [from, to], quote: block.textContent.trim().replace(/\s+/g, ' ').slice(0, 200) });
      compose();
    });
  }

  // A cell or an output is commented on as a whole by its label; what an
  // HTML output shows is in a frame that runs no script, so nothing in it
  // can be pointed at.
  if (kind === 'notebook') {
    content.addEventListener('click', (event) => {
      const label = event.target.closest('.label');
      if (!label) return;
      const output = label.closest('.output');
      selection = cellAnchor(label.closest('section.cell'));
      if (output) selection.output = Number(output.dataset.output);
      unselect();
      label.classList.add('sel');
      compose();
    });
    // The frame has the server's origin but runs nothing, so the viewer
    // reads how tall its document is.
    const fit = (frame) => {
      const doc = frame.contentDocument;
      if (doc && doc.documentElement) frame.style.height = doc.documentElement.scrollHeight + 'px';
    };
    for (const frame of content.querySelectorAll('iframe.html')) {
      frame.addEventListener('load', () => {
        fit(frame);
        for (const image of frame.contentDocument.images) image.addEventListener('load', () => fit(frame));
      });
    }
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

  // A comment on a video or a sound is at the moment where it is paused.
  if (kind === 'media') {
    const media = document.getElementById('media');
    const pick = () => {
      if (!media.paused) return;
      selection = { time: Math.round(media.currentTime * 10) / 10 };
      compose();
    };
    media.addEventListener('pause', pick);
    media.addEventListener('seeked', pick);
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
    unselect();
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
      hint.textContent = { text: 'Click a line number to comment on it; shift-click to extend the range.', markdown: 'Click a paragraph, heading or list item to comment on it.', image: 'Click a spot, or drag over an area, to comment on it.', media: 'Pause where you want to comment; the comment is at that moment.', notebook: 'Click the label of a cell or an output to comment on it, or a line number of code or a paragraph of Markdown for a part of a cell.', other: 'Comment on the file as a whole.' }[kind];
      list.append(hint);
    }
  }

  function mark(comment, canvas) {
    const a = comment.anchor;
    const scope = scopeOf(a);
    if (!scope) return;
    if (a.output) {
      const label = scope.querySelector('.output[data-output="' + a.output + '"] > .label');
      if (label) label.classList.add('has');
    } else if (a.lines) {
      for (let n = a.lines[0]; n <= a.lines[1]; n++) {
        const r = row(scope, n);
        if (r) r.querySelector('.n').classList.add('has');
      }
      // The innermost blocks over the lines carry the mark, not the list
      // or quote that contains them as well.
      for (const block of blocksOver(scope, a.lines)) block.classList.add('has');
    } else if (a.cell) {
      scope.querySelector(':scope > .label').classList.add('has');
    } else if ((a.point || a.rect) && canvas) {
      place(canvas, a, 'mark' + (comment.resolved ? ' resolved' : ''));
    }
  }

  function blocksOver(scope, lines) {
    const over = (block) => {
      const [from, to] = block.dataset.lines.split('-').map(Number);
      return from <= lines[1] && to >= lines[0] && !block.closest('.output');
    };
    return Array.from(scope.querySelectorAll('[data-lines]')).filter((block) =>
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
    const scope = scopeOf(anchor);
    if (!scope) return;
    if (anchor.output) {
      target = scope.querySelector('.output[data-output="' + anchor.output + '"]');
    } else if (anchor.lines) {
      target = row(scope, anchor.lines[0]) || blocksOver(scope, anchor.lines)[0] || null;
    } else if (anchor.cell) {
      target = scope;
    } else if (anchor.point || anchor.rect) {
      target = content.querySelector('.canvas');
    } else if (anchor.time !== undefined) {
      target = document.getElementById('media');
      target.pause();
      target.currentTime = anchor.time;
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
