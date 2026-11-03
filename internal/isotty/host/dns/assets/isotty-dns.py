#!/usr/bin/env python3
"""Change DNS records through the IsoTTY gateway.

The gateway runs on the host inside `isotty attach` and calls Cloud DNS with
the human's gcloud login, under the names pinned in
.isotty/dns.yaml. No credential enters this VM.
"""
import argparse
import json
import sys
import urllib.error
import urllib.parse
import urllib.request

GATEWAY = 'http://127.0.0.1:__ISOTTY_GATEWAY_PORT__/dns'

UNREACHABLE = (
    'isotty-dns: the DNS gateway at {gateway} is not reachable ({reason}). '
    'It is available only while the host is attached with `isotty attach`. '
    'Retry after re-attach.'
)


def call(method, path, body=None, params=None):
    if params:
        path += '?' + urllib.parse.urlencode(params)
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(GATEWAY + path, data=data, method=method, headers={'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(req, timeout=120) as res:
            return json.load(res)
    except urllib.error.HTTPError as e:
        error = json.load(e)['error']
        if isinstance(error, str):
            # The gateway's own refusal.
            raise SystemExit('isotty-dns: ' + error)
        # Cloud DNS's refusal, relayed as is.
        raise SystemExit(f"isotty-dns: Cloud DNS answered {error['status']}: {error['message']}")
    except urllib.error.URLError as e:
        raise SystemExit(UNREACHABLE.format(gateway=GATEWAY, reason=e.reason))


def print_records(records):
    for record in records:
        print(f"{record['name']}\t{record['type']}\t{record['ttl']}\t{' '.join(record['rrdatas'])}")


def cmd_status(args):
    status = call('GET', '/status')
    print(f"DNS records change through the isotty gateway ({status['provider']}). This project may change:")
    for zone in status['zones']:
        print(f"  {zone['project']}/{zone['managed_zone']}: {', '.join(zone['names'])} and the names under them")


def cmd_list(args):
    params = {'name': args.name} if args.name else None
    print_records(call('GET', '/list', params=params)['records'])


def cmd_set(args):
    body = {'name': args.name, 'type': args.type, 'rrdatas': args.rrdata, 'ttl': args.ttl}
    answer = call('POST', '/set', body)
    print(f"# {answer['status']}", file=sys.stderr)
    print_records([answer['record']])


def cmd_delete(args):
    answer = call('POST', '/delete', {'name': args.name, 'type': args.type})
    print(f"# {answer['status']}, was:", file=sys.stderr)
    print_records([answer['record']])


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command_name', required=True)
    p = sub.add_parser('status', help='check that the gateway is reachable and show what this project may change')
    p.set_defaults(func=cmd_status)
    p = sub.add_parser('list', help='show the records under the pinned names, or under one name')
    p.add_argument('name', nargs='?')
    p.set_defaults(func=cmd_list)
    p = sub.add_parser('set', help='create a record, or replace the one with the same name and type')
    p.add_argument('type', help='record type, for example A or TXT')
    p.add_argument('name', help='record name, for example app.lab.example.org')
    p.add_argument('rrdata', nargs='+', help='record data, one argument per value')
    p.add_argument('--ttl', type=int, default=300)
    p.set_defaults(func=cmd_set)
    p = sub.add_parser('delete', help='delete the record with this name and type')
    p.add_argument('type')
    p.add_argument('name')
    p.set_defaults(func=cmd_delete)
    args = parser.parse_args()
    args.func(args)


if __name__ == '__main__':
    main()
