#!/usr/bin/env python3
"""Read and write ep_weave pads through the IsoTTY gateway.

The gateway runs on the host inside `isotty attach` and is reachable only
while the host is attached. Writes are not queued: a failed write did not
happen.
"""
import argparse
import json
import sys
import urllib.error
import urllib.parse
import urllib.request

GATEWAY = 'http://127.0.0.1:__ISOTTY_GATEWAY_PORT__/epweave'

UNREACHABLE = (
    'isotty-notes: the ep_weave gateway at {gateway} is not reachable ({reason}). '
    'It is available only while the host is attached with `isotty attach`; writes are not queued. '
    'Keep the note as a Markdown file under /workspace and retry after re-attach.'
)


def call(method, path, params=None, body=None):
    url = GATEWAY + path
    if params:
        url += '?' + urllib.parse.urlencode(params)
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode()
        headers['content-type'] = 'application/json'
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=120) as res:
            return json.load(res)
    except urllib.error.HTTPError as e:
        # The gateway always answers errors as {"error": ...}.
        raise SystemExit('isotty-notes: ' + json.load(e)['error'])
    except urllib.error.URLError as e:
        raise SystemExit(UNREACHABLE.format(gateway=GATEWAY, reason=e.reason))


def cmd_status(args):
    status = call('GET', '/status')
    print(f"ep_weave is reachable through the isotty gateway; writable title prefix: {status['prefix']}")
    print(f"pad URLs such as {status['url']}/p/<id> are for humans on the host; this VM cannot open them")


def cmd_search(args):
    for d in call('GET', '/search', {'q': args.query, 'rows': args.rows})['docs']:
        print(f"{d['id']}\t{d['title']}")


def cmd_read(args):
    result = call('GET', '/read', {'title': args.title, 'format': 'html' if args.html else 'text'})
    print(f"# pad {result['pad_id']} rev {result['revisions']}", file=sys.stderr)
    print(result['content'])


def report_write(result):
    print(f"# pad {result['pad_id']} rev {result['revisions']}", file=sys.stderr)
    print(result['url'])


def cmd_create(args):
    body = {'title': args.title, 'markdown': sys.stdin.read(), 'tags': args.tags or ''}
    report_write(call('POST', '/create', body=body))


def cmd_append(args):
    body = {'title': args.title, 'markdown': sys.stdin.read()}
    report_write(call('POST', '/append', body=body))


def cmd_changes(args):
    params = {'title': args.title, 'since': args.since, 'format': 'html' if args.html else 'text'}
    result = call('GET', '/changes', params)
    if result['from_rev'] >= result['to_rev']:
        print(f"# pad {result['pad_id']}: no changes since rev {args.since} (current {result['to_rev']})", file=sys.stderr)
        return
    authors = ', '.join(sorted(result['authors'].values()))
    print(f"# pad {result['pad_id']} rev {result['from_rev']} -> {result['to_rev']}; authors: {authors}", file=sys.stderr)
    if args.html:
        print(result['html'])
    else:
        print('\n'.join(result['added_lines']))


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command', required=True)
    p = sub.add_parser('status', help='check that the gateway is reachable and show the writable prefix')
    p.set_defaults(func=cmd_status)
    p = sub.add_parser('search', help='search pads (ep_search query syntax, e.g. hash:"#tag" or title:foo*)')
    p.add_argument('query')
    p.add_argument('--rows', type=int, default=20)
    p.set_defaults(func=cmd_search)
    p = sub.add_parser('read', help='print a pad by title')
    p.add_argument('title')
    p.add_argument('--html', action='store_true')
    p.set_defaults(func=cmd_read)
    p = sub.add_parser('create', help='create a pad from markdown on stdin')
    p.add_argument('title')
    p.add_argument('--tags', help='hashtags for the last line, e.g. "#isotty #worklog"')
    p.set_defaults(func=cmd_create)
    p = sub.add_parser('append', help='append markdown from stdin to an existing pad')
    p.add_argument('title')
    p.set_defaults(func=cmd_append)
    p = sub.add_parser('changes', help='show the lines added since a revision, with authors')
    p.add_argument('title')
    p.add_argument('--since', type=int, required=True)
    p.add_argument('--html', action='store_true', help='print the full diff HTML instead of the added lines')
    p.set_defaults(func=cmd_changes)
    args = parser.parse_args()
    args.func(args)


if __name__ == '__main__':
    main()
