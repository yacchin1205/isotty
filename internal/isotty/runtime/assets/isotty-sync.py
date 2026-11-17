#!/usr/bin/env python3
"""Tell how the sync of /workspace from the host stands.

The attach that holds the host gateway writes the state to
__ISOTTY_SYNC_STATE__ every __ISOTTY_SYNC_INTERVAL__ seconds until the first
sync is done. While no such attach runs, nobody writes it, and a state older
than __ISOTTY_SYNC_STALE__ seconds says only how it stood then.
"""
import argparse
import json
import os
import sys
import time

STATE = '__ISOTTY_SYNC_STATE__'
STALE = int('__ISOTTY_SYNC_STALE__')


def read():
    """The state and its age in seconds, or (None, None) before the first one."""
    try:
        with open(STATE, encoding='utf-8') as f:
            state = json.load(f)
    except FileNotFoundError:
        return None, None
    return state, time.time() - os.stat(STATE).st_mtime


def describe(state, age):
    if state is None:
        return 'no sync state has been written yet'
    if state['done']:
        return 'done'
    line = 'syncing: ' + state['status']
    if state.get('expected_files'):
        line += f" ({state['received_files']}/{state['expected_files']} files staged)"
    if age > STALE:
        line += f'; not updated for {int(age)} seconds, since the host is not attached; waiting for it to attach again'
    return line


def cmd_status(_args):
    print(describe(*read()))


def cmd_wait(_args):
    last = None
    while True:
        state, age = read()
        line = describe(state, age)
        if line != last:
            print(line, flush=True)
            last = line
        if state is not None and state['done']:
            return
        time.sleep(5)


def cmd_hook(_args):
    """UserPromptSubmit hook of Claude Code and Codex: says nothing once the
    first sync is done."""
    state, age = read()
    if state is None or state['done']:
        return
    context = (f'isotty: /workspace is not fully synced from the host yet ({describe(state, age)}). '
               'Files may be missing until it is; run `isotty-sync wait` before relying on them.')
    json.dump({'hookSpecificOutput': {'hookEventName': 'UserPromptSubmit', 'additionalContext': context}}, sys.stdout)


def main():
    parser = argparse.ArgumentParser(prog='isotty-sync', description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command', required=True)
    sub.add_parser('status', help='how the sync stands').set_defaults(func=cmd_status)
    sub.add_parser('wait', help='wait until the first sync is done').set_defaults(func=cmd_wait)
    sub.add_parser('hook', help='the agents\' prompt hook').set_defaults(func=cmd_hook)
    args = parser.parse_args()
    args.func(args)


if __name__ == '__main__':
    main()
