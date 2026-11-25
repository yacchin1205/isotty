#!/usr/bin/env python3
"""Ask the host to open a URL, or a file of this workspace, for the human,
and read the comments the human left on files shown that way.

http and https URLs open in the human's browser. A path under the workspace
opens the host's synced copy of that file in a viewer where the human can
comment on a line, a block, a spot of an image, a notebook's cell or
output, or the file as a whole. The
gateway is there while the host is reachable. Exit status and stderr carry the
reason when nothing opened.

The comments come back through the sync, as .isotty/reviews/<path>.jsonl
under the workspace; `isotty-open reviews` reads them.
"""
import glob
import hashlib
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

GATEWAY = 'http://127.0.0.1:__ISOTTY_GATEWAY_PORT__'

REVIEWS = os.path.join('.isotty', 'reviews')


def open_target(target):
    if urllib.parse.urlsplit(target).scheme.lower() in ('http', 'https'):
        body = {'url': target}
    else:
        body = {'path': os.path.abspath(target)}
    req = urllib.request.Request(GATEWAY + '/open', data=json.dumps(body).encode(),
                                 headers={'content-type': 'application/json'}, method='POST')
    try:
        with urllib.request.urlopen(req, timeout=60):
            return
    except urllib.error.HTTPError as e:
        raise SystemExit('isotty-open: ' + json.load(e)['error'])
    except urllib.error.URLError as e:
        raise SystemExit(f'isotty-open: the host is not reachable ({e.reason}): it is asleep or offline, '
                         'or its isotty daemon is not running; give the URL or path to the human instead')


def workspace_of(path):
    """The workspace holding path: the nearest directory up from it with an
    .isotty directory, which is where the sync puts the comments."""
    here = os.path.abspath(path)
    while True:
        if os.path.isdir(os.path.join(here, '.isotty')):
            return here
        parent = os.path.dirname(here)
        if parent == here:
            raise SystemExit(f'isotty-open: {path} is not under a workspace with an .isotty directory')
        here = parent


def file_sha256(path):
    digest = hashlib.sha256()
    try:
        with open(path, 'rb') as f:
            for chunk in iter(lambda: f.read(1 << 20), b''):
                digest.update(chunk)
    except OSError:
        return None
    return 'sha256:' + digest.hexdigest()


def lines_of(anchor):
    a, b = anchor['lines']
    return f'L{a}' if a == b else f'L{a}-{b}'


def where(anchor):
    if 'cell' in anchor:
        place = f"cell {anchor['cell']}"
        if anchor.get('cell_id'):
            place += f" (id {anchor['cell_id']})"
        if 'output' in anchor:
            return place + f" output {anchor['output']}"
        if 'lines' in anchor:
            return place + ' ' + lines_of(anchor)
        return place
    if 'lines' in anchor:
        return lines_of(anchor)
    if 'point' in anchor:
        return '({:.0%}, {:.0%})'.format(*anchor['point'])
    if 'rect' in anchor:
        x, y, w, h = anchor['rect']
        return f'area ({x:.0%}, {y:.0%}) {w:.0%}x{h:.0%}'
    if 'time' in anchor:
        minutes, seconds = divmod(anchor['time'], 60)
        return f'at {int(minutes)}:{seconds:04.1f}'
    return 'whole file'


def notebook_cells(path):
    """The cells of a notebook as (id, lines of its source), or None when
    it is not one that can be read."""
    try:
        with open(path, encoding='utf-8') as f:
            cells = json.load(f)['cells']
        return [(c.get('id', ''), ''.join(c['source']) if isinstance(c['source'], list) else c['source']) for c in cells]
    except (OSError, ValueError, KeyError, TypeError):
        return None


def moved(anchor, path, cache):
    """Where the place a comment names is now, in a file changed since."""
    notes = []
    if 'cell' in anchor:
        if 'cells' not in cache:
            cache['cells'] = notebook_cells(path)
        cells = cache['cells']
        if cells is None:
            return ['not readable as a notebook now']
        if anchor.get('cell_id'):
            now = [i + 1 for i, (cell_id, _) in enumerate(cells) if cell_id == anchor['cell_id']]
            notes.append(f"that cell is now cell {now[0]}" if now else 'that cell is gone')
        if 'lines' in anchor and anchor.get('quote'):
            hits = [f'cell {i + 1} L{n + 1}' for i, (_, source) in enumerate(cells)
                    for n, line in enumerate(source.split('\n')) if anchor['quote'] in line]
            if hits:
                notes.append('quoted text now at ' + ', '.join(hits))
        return notes
    # The quoted text finds the place again when the lines moved.
    if 'lines' in anchor and anchor.get('quote'):
        if 'lines' not in cache:
            with open(path, encoding='utf-8', errors='replace') as f:
                cache['lines'] = f.read().split('\n')
        hits = [i + 1 for i, line in enumerate(cache['lines']) if anchor['quote'] in line]
        if hits:
            notes.append('quoted text now at L' + ', L'.join(str(h) for h in hits))
    return notes


def cmd_reviews(args):
    show_all = '--all' in args
    paths = [a for a in args if a != '--all']
    if len(paths) > 1:
        raise SystemExit('usage: isotty-open reviews [--all] [PATH]')
    workspace = workspace_of(paths[0] if paths else os.getcwd())
    root = os.path.join(workspace, REVIEWS)
    if paths:
        rel = os.path.relpath(os.path.abspath(paths[0]), workspace)
        records = [os.path.join(root, rel + '.jsonl')]
    else:
        records = sorted(glob.glob(os.path.join(root, '**', '*.jsonl'), recursive=True))
    shown = hidden = 0
    for record in records:
        rel = os.path.relpath(record, root)[:-len('.jsonl')]
        try:
            with open(record, encoding='utf-8') as f:
                comments = [json.loads(line) for line in f if line.strip()]
        except FileNotFoundError:
            continue
        current = file_sha256(os.path.join(workspace, rel))
        cache = {}
        printed_file = False
        for comment in comments:
            if comment.get('resolved') and not show_all:
                hidden += 1
                continue
            if not printed_file:
                print(rel)
                printed_file = True
            shown += 1
            anchor = comment.get('anchor', {})
            notes = []
            if comment.get('resolved'):
                notes.append('resolved')
            if current is None:
                notes.append('file is gone')
            elif comment.get('file_sha256') != current:
                notes.append('file changed since')
                notes.extend(moved(anchor, os.path.join(workspace, rel), cache))
            head = f"  {where(anchor)}  {comment.get('at', '')}"
            if anchor.get('quote'):
                head += '  "' + anchor['quote'] + '"'
            if notes:
                head += '  [' + '; '.join(notes) + ']'
            print(head)
            for line in comment.get('body', '').split('\n'):
                print('    ' + line)
    summary = f'{shown} comment{"s" if shown != 1 else ""}'
    if hidden:
        summary += f'; {hidden} resolved not shown (--all shows them)'
    print(summary, file=sys.stderr)


def main():
    if len(sys.argv) >= 2 and sys.argv[1] == 'reviews':
        cmd_reviews(sys.argv[2:])
        return
    if len(sys.argv) != 2:
        raise SystemExit('usage: isotty-open URL|PATH\n       isotty-open reviews [--all] [PATH]')
    open_target(sys.argv[1])


if __name__ == '__main__':
    main()
