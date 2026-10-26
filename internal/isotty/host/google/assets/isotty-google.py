#!/usr/bin/env python3
"""Run Google-API tools as the project's service account through the IsoTTY gateway.

The gateway runs on the host inside `isotty attach` and hands out one-hour
access tokens for the service account and scopes pinned in
.isotty/google.yaml. The key never enters this VM. A token already issued
keeps working until it expires; after that the host must be attached again.
"""
import argparse
import json
import os
import sys
import urllib.error
import urllib.request

GATEWAY = 'http://127.0.0.1:__ISOTTY_GATEWAY_PORT__/google'
TOKEN_ENV = 'GOOGLE_OAUTH_ACCESS_TOKEN'

UNREACHABLE = (
    'isotty-google: the Google token gateway at {gateway} is not reachable ({reason}). '
    'It is available only while the host is attached with `isotty attach`. '
    'Retry after re-attach.'
)


def call(path):
    try:
        with urllib.request.urlopen(GATEWAY + path, timeout=60) as res:
            return json.load(res)
    except urllib.error.HTTPError as e:
        # The gateway always answers errors as {"error": ...}.
        raise SystemExit('isotty-google: ' + json.load(e)['error'])
    except urllib.error.URLError as e:
        raise SystemExit(UNREACHABLE.format(gateway=GATEWAY, reason=e.reason))


def cmd_status(args):
    status = call('/status')
    print(f"Google tokens are available through the isotty gateway as {status['service_account']}")
    print('scopes: ' + ', '.join(status['scopes']))


def cmd_token(args):
    print(call('/token')['access_token'])


def cmd_exec(args):
    if not args.command:
        raise SystemExit('isotty-google: exec needs a command after --')
    token = call('/token')
    env = dict(os.environ)
    env[TOKEN_ENV] = token['access_token']
    # Nothing else in the VM may answer as a Google credential.
    env.pop('GOOGLE_APPLICATION_CREDENTIALS', None)
    os.execvpe(args.command[0], args.command, env)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command_name', required=True)
    p = sub.add_parser('status', help='check that the gateway is reachable and show the service account and scopes')
    p.set_defaults(func=cmd_status)
    p = sub.add_parser('token', help='print an access token, for a tool that cannot read it from the environment')
    p.set_defaults(func=cmd_token)
    p = sub.add_parser('exec', help=f'run a command with {TOKEN_ENV} set: isotty-google exec -- <command> [arguments]')
    p.add_argument('command', nargs=argparse.REMAINDER)
    p.set_defaults(func=cmd_exec)
    args = parser.parse_args()
    if getattr(args, 'command', None) and args.command[0] == '--':
        args.command = args.command[1:]
    args.func(args)


if __name__ == '__main__':
    main()
