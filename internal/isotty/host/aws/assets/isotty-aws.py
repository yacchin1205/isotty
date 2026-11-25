#!/usr/bin/env python3
"""Run AWS tools as the project's IAM role through the IsoTTY gateway.

The gateway runs in the host's isotty daemon and hands out one-hour
credentials of the role pinned in .isotty/aws.yaml, assumed with the human's
AWS login, which never enters this VM. `exec` points the AWS CLI and SDKs
at the gateway, and they fetch credentials again before they expire. While
the host is not reachable, credentials already fetched keep working until
they expire.
"""
import argparse
import json
import os
import sys
import urllib.error
import urllib.request

GATEWAY = 'http://127.0.0.1:__ISOTTY_GATEWAY_PORT__/aws'
ENDPOINT_ENV = 'AWS_CONTAINER_CREDENTIALS_FULL_URI'
# Variables through which something else would answer as an AWS identity
# before the endpoint does.
OTHER_IDENTITIES = (
    'AWS_ACCESS_KEY_ID', 'AWS_SECRET_ACCESS_KEY', 'AWS_SESSION_TOKEN', 'AWS_SECURITY_TOKEN',
    'AWS_PROFILE', 'AWS_DEFAULT_PROFILE',
    'AWS_CONTAINER_CREDENTIALS_RELATIVE_URI', 'AWS_CONTAINER_AUTHORIZATION_TOKEN',
    'AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE', 'AWS_WEB_IDENTITY_TOKEN_FILE', 'AWS_ROLE_ARN',
)

UNREACHABLE = (
    'isotty-aws: the AWS credentials gateway at {gateway} is not reachable ({reason}). '
    'The host is asleep or offline, or its isotty daemon is not running; nothing is queued. '
    'Keep your work under /workspace, retry once the host is back, and tell the human.'
)


def call(path):
    try:
        with urllib.request.urlopen(GATEWAY + path, timeout=60) as res:
            return json.load(res)
    except urllib.error.HTTPError as e:
        # The gateway always answers errors as {"error": ...}.
        raise SystemExit('isotty-aws: ' + json.load(e)['error'])
    except urllib.error.URLError as e:
        raise SystemExit(UNREACHABLE.format(gateway=GATEWAY, reason=e.reason))


def cmd_status(args):
    status = call('/status')
    print(f"AWS credentials are available through the isotty gateway as {status['role_arn']}")
    print(f"account: {status['account']}")
    print(f"session: {status['session']}")


def cmd_exec(args):
    if not args.command:
        raise SystemExit('isotty-aws: exec needs a command after --')
    # Fail now, with the reason, rather than inside the tool.
    call('/credentials')
    env = dict(os.environ)
    for name in OTHER_IDENTITIES:
        env.pop(name, None)
    env[ENDPOINT_ENV] = GATEWAY + '/credentials'
    os.execvpe(args.command[0], args.command, env)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command_name', required=True)
    p = sub.add_parser('status', help='check that the gateway is reachable and show the role')
    p.set_defaults(func=cmd_status)
    p = sub.add_parser('exec', help=f'run a command with {ENDPOINT_ENV} set: isotty-aws exec -- <command> [arguments]')
    p.add_argument('command', nargs=argparse.REMAINDER)
    p.set_defaults(func=cmd_exec)
    args = parser.parse_args()
    if getattr(args, 'command', None) and args.command[0] == '--':
        args.command = args.command[1:]
    args.func(args)


if __name__ == '__main__':
    main()
