#!/usr/bin/env python3
"""Run functions of the project's Apps Script through the IsoTTY gateway.

The gateway runs on the host inside `isotty attach` and calls scripts.run as
the human, for the scripts pinned in .isotty/gas.yaml. The human's
credential never enters this VM.
"""
import argparse
import json
import sys
import urllib.error
import urllib.request

GATEWAY = 'http://127.0.0.1:__ISOTTY_GATEWAY_PORT__/gas'
# Apps Script lets one run last six minutes; the gateway waits seven.
RUN_TIMEOUT = 7 * 60 + 30

UNREACHABLE = (
    'isotty-gas: the Apps Script gateway at {gateway} is not reachable ({reason}). '
    'It is available only while the host is attached with `isotty attach`. '
    'Retry after re-attach.'
)


def call(method, path, body=None, timeout=60):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(GATEWAY + path, data=data, method=method, headers={'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as res:
            return json.load(res)
    except urllib.error.HTTPError as e:
        error = json.load(e)['error']
        if isinstance(error, str):
            # The gateway's own refusal.
            raise SystemExit('isotty-gas: ' + error)
        # The Apps Script API's refusal, relayed as is.
        raise SystemExit(f"isotty-gas: the Apps Script API answered {e.code}: {error['message']}")
    except urllib.error.URLError as e:
        raise SystemExit(UNREACHABLE.format(gateway=GATEWAY, reason=e.reason))


def cmd_status(args):
    status = call('GET', '/status')
    print('Apps Script functions run through the isotty gateway. Pinned scripts:')
    for script in status['scripts']:
        print(f"  {script['id']} as {script['account']}")
        print('    scopes: ' + ', '.join(script['scopes']))


def cmd_run(args):
    parameters = []
    if args.parameters is not None:
        parameters = json.loads(args.parameters)
        if not isinstance(parameters, list):
            raise SystemExit('isotty-gas: parameters must be a JSON array, one element per function argument')
    answer = call('POST', '/run', {'script': args.script, 'function': args.function, 'parameters': parameters}, timeout=RUN_TIMEOUT)
    if 'error' in answer:
        error = answer['error']
        lines = [f"isotty-gas: {args.function} failed: {error['message']}"]
        if 'details' in error:
            for detail in error['details']:
                lines.append(f"  {detail['errorType']}: {detail['errorMessage']}")
                if 'scriptStackTraceElements' in detail:
                    for frame in detail['scriptStackTraceElements']:
                        lines.append(f"    at {frame['function']} (line {frame['lineNumber']})")
        raise SystemExit('\n'.join(lines))
    response = answer['response']
    if 'result' in response:
        print(json.dumps(response['result'], ensure_ascii=False, indent=2))


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command_name', required=True)
    p = sub.add_parser('status', help='check that the gateway is reachable and show the scripts and account')
    p.set_defaults(func=cmd_status)
    p = sub.add_parser('run', help='run a function of a pinned script and print its return value as JSON')
    p.add_argument('script', help='id of a pinned script, one of those `status` lists')
    p.add_argument('function', help='function name')
    p.add_argument('parameters', nargs='?', help='JSON array of arguments, for example \'["sheet-id", 3]\'')
    p.set_defaults(func=cmd_run)
    args = parser.parse_args()
    args.func(args)


if __name__ == '__main__':
    main()
