#!/usr/bin/env python3
"""Ask the host to wake this conversation later.

`isotty-wake in 1h "<prompt>"` asks the host to resume this agent
conversation with the prompt an hour from now. End the turn after asking:
with no work left the VM powers itself off, and the host starts it again at
that time, provided the human allowed wakes with `isotty schedule enable`.
The conversation goes on where it stopped, in a new tmux window when its
agent has exited. `isotty-wake every "0 6 * * *" "<prompt>"` does so at each
time the cron fields name, skipping a time when the conversation still has
work, until the wake is cancelled.
"""
import argparse
import base64
import json
import os
import re
import shlex
import subprocess
import sys
import urllib.error
import urllib.request

GATEWAY = 'http://127.0.0.1:__ISOTTY_GATEWAY_PORT__/wake'
IDLE_STATE = '__ISOTTY_IDLE_STATE__'
WORKSPACE = '/workspace'
TMUX_SESSION = 'isotty'
BROWSER = '__ISOTTY_OPEN_CLIENT__'
# deliver's exit status for a conversation that still has work.
BUSY = 75
UNITS = {'s': 1, 'm': 60, 'h': 3600, 'd': 86400}


def parse_after(text):
    m = re.fullmatch(r'(\d+)([smhd])', text)
    if not m or int(m.group(1)) == 0:
        raise SystemExit(f'isotty-wake: {text!r} is not a time such as 30m, 1h or 2d')
    return int(m.group(1)) * UNITS[m.group(2)]


def parent(pid):
    with open(f'/proc/{pid}/stat', encoding='utf-8') as f:
        stat = f.read()
    return int(stat[stat.rindex(')') + 2:].split()[1])


def ancestors(pid):
    while pid > 1:
        yield pid
        pid = parent(pid)


def sessions():
    """The agent sessions isotty-idle knows, by the pid of their agent."""
    found = {}
    if not os.path.isdir(IDLE_STATE):
        return found
    for name in os.listdir(IDLE_STATE):
        if name.endswith('.json'):
            with open(os.path.join(IDLE_STATE, name), encoding='utf-8') as f:
                state = json.load(f)
            found[state['pid']] = state
    return found


def own_session():
    known = sessions()
    for pid in ancestors(os.getpid()):
        if pid in known:
            return known[pid]
    raise SystemExit('isotty-wake: not run by an agent session that isotty knows; '
                     'run it from the agent, after it has had a prompt; Codex runs isotty\'s hooks only once '
                     'the human has reviewed them with /hooks')


def call(method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    request = urllib.request.Request(GATEWAY + path, data=data, method=method, headers={'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            body = response.read()
            return json.loads(body) if body else None
    except urllib.error.HTTPError as e:
        raise SystemExit(f'isotty-wake: {json.load(e).get("error", e.reason)}')
    except urllib.error.URLError as e:
        raise SystemExit(f'isotty-wake: the host is not reachable ({e.reason}); '
                         'it is asleep or offline, or its isotty daemon is not running, and nothing is queued. '
                         'Ask again once the host is back, or tell the human rather than wait')


def cmd_in(args):
    session = own_session()
    wake = call('POST', '/wakes', {'agent': session['agent'], 'session': session['session'],
                                   'after_seconds': parse_after(args.after), 'prompt': args.prompt})
    print(f"wake {wake['id']} at {wake['at']}: this conversation resumes with the prompt then. "
          'End the turn now; the VM may power off meanwhile.')


def cmd_every(args):
    session = own_session()
    wake = call('POST', '/wakes', {'agent': session['agent'], 'session': session['session'],
                                   'cron': args.cron, 'prompt': args.prompt})
    print(f"wake {wake['id']} on {wake['cron']}, next at {wake['at']}: this conversation resumes with the prompt "
          'each time, unless it still has work then. End the turn now; the VM may power off meanwhile.')


def cmd_list(_args):
    for wake in call('GET', '/wakes'):
        when = f"every {wake['cron']}, next " if wake.get('cron') else ''
        print(f"{wake['id']}  {when}{wake['at']}  {wake['agent']} {wake['session']}  {wake['prompt']}")


def cmd_cancel(args):
    call('DELETE', '/wakes/' + args.id)
    print(f'wake {args.id} cancelled')


def pane_of(pid):
    """The tmux pane whose process is pid or one of its ancestors."""
    out = subprocess.run(['tmux', 'list-panes', '-a', '-F', '#{pane_id} #{pane_pid}'],
                         capture_output=True, text=True)
    if out.returncode != 0:
        return None
    panes = dict(reversed(line.split()) for line in out.stdout.split('\n') if line)
    for ancestor in ancestors(pid):
        if str(ancestor) in panes:
            return panes[str(ancestor)]
    return None


def alive(state):
    try:
        with open(f"/proc/{state['pid']}/stat", encoding='utf-8') as f:
            stat = f.read()
    except FileNotFoundError:
        return False
    return stat[stat.rindex(')') + 2:].split()[19] == state['pid_start']


def resume_command(agent, session, prompt):
    """The agent's own way to resume a conversation with a first message.
    Codex runs without its shared background server: the one it left
    before the VM powered off is gone, and its state with it."""
    if agent == 'claude':
        return ['claude', '--resume', session, prompt]
    if agent == 'codex':
        return ['codex', 'resume', '--no-daemon', session, prompt]
    raise SystemExit(f'isotty-wake: cannot resume {agent}')


def cmd_deliver(args):
    """Run by the host: give the prompt to the conversation, typed into its
    pane when its agent still runs, else to the agent resumed in a new
    window."""
    prompt = base64.b64decode(args.prompt).decode()
    for state in sessions().values():
        if state['session'] == args.session and state['state'] in ('working', 'waiting') and alive(state):
            # The conversation still has work, or waits for the human to
            # approve something: keys typed now would cut into the work, or
            # answer the approval in the human's place.
            print('the conversation still has work')
            sys.exit(BUSY)
    for state in sessions().values():
        if state['session'] == args.session and alive(state):
            pane = pane_of(state['pid'])
            if pane is None:
                raise SystemExit(f'isotty-wake: the agent of {args.session} runs outside tmux')
            subprocess.run(['tmux', 'send-keys', '-t', pane, '-l', '--', prompt], check=True)
            subprocess.run(['tmux', 'send-keys', '-t', pane, 'Enter'], check=True)
            print(f'typed into {pane}')
            return
    if subprocess.run(['tmux', 'has-session', '-t', TMUX_SESSION], capture_output=True).returncode != 0:
        # As the attach does, so what the agent opens reaches the host.
        subprocess.run(['tmux', 'new-session', '-d', '-s', TMUX_SESSION, '-c', WORKSPACE,
                        ';', 'set-environment', '-t', TMUX_SESSION, 'BROWSER', BROWSER], check=True)
    command = shlex.join(resume_command(args.agent, args.session, prompt))
    subprocess.run(['tmux', 'new-window', '-t', TMUX_SESSION, '-c', WORKSPACE, '-e', 'BROWSER=' + BROWSER,
                    'bash', '-lc', command], check=True)
    # Whether the agent took the conversation up is for its window to say.
    print(f'started {shlex.join(resume_command(args.agent, args.session, "…"))} in a new window')


def main():
    parser = argparse.ArgumentParser(prog='isotty-wake', description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command', required=True)
    p = sub.add_parser('in', help='resume this conversation with PROMPT after AFTER (30m, 1h, 2d)')
    p.add_argument('after')
    p.add_argument('prompt')
    p.set_defaults(func=cmd_in)
    p = sub.add_parser('every', help='resume this conversation with PROMPT at each time CRON names: five fields, the host\'s local time')
    p.add_argument('cron')
    p.add_argument('prompt')
    p.set_defaults(func=cmd_every)
    sub.add_parser('list', help='the wakes the host keeps').set_defaults(func=cmd_list)
    p = sub.add_parser('cancel', help='forget a wake')
    p.add_argument('id')
    p.set_defaults(func=cmd_cancel)
    p = sub.add_parser('deliver', help=argparse.SUPPRESS)
    p.add_argument('agent')
    p.add_argument('session')
    p.add_argument('prompt', help='base64')
    p.set_defaults(func=cmd_deliver)
    args = parser.parse_args()
    args.func(args)


if __name__ == '__main__':
    main()
