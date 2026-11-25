#!/usr/bin/env python3
"""Power the VM off once no agent has had work for a while.

The agents' hooks call `isotty-idle hook <agent>` as a session starts a
turn, ends one, is interrupted or ends; each session's state is a file
under __ISOTTY_IDLE_STATE__. `isotty-idle check`, run every minute by a
systemd timer, powers the VM off when no live session has work and none
has had any for the time in __ISOTTY_IDLE_CONFIG__. A session has work
from a prompt until its turn ends, and after that while Claude Code still
has background tasks or scheduled prompts that will wake it.
"""
import argparse
import json
import os
import pwd
import subprocess
import sys
import time

STATE_DIR = '__ISOTTY_IDLE_STATE__'
CONFIG = '__ISOTTY_IDLE_CONFIG__'
LAST_WORK = '__ISOTTY_IDLE_LAST_WORK__'
EVENTS = '__ISOTTY_EVENTS__'
OPECO_SESSION = '__ISOTTY_OPECO_SESSION__'
OPECO_DIR = '__ISOTTY_OPECO_DIR__'
KERNEL_STAT = '__ISOTTY_KERNEL_STAT__'

WORKING, WAITING, IDLE = 'working', 'waiting', 'idle'
SHELLS = {'sh', 'bash', 'dash', 'zsh'}


def proc_stat(pid):
    """The command name, parent and start time of a process, or None."""
    try:
        with open(f'/proc/{pid}/stat', encoding='utf-8') as f:
            stat = f.read()
    except FileNotFoundError:
        return None
    # The name is in parentheses and may hold spaces; the fields follow it.
    name = stat[stat.index('(') + 1:stat.rindex(')')]
    fields = stat[stat.rindex(')') + 2:].split()
    return name, int(fields[1]), fields[19]


def agent_process():
    """The agent that runs the hook: the first ancestor that is not a
    shell, since an agent may run its hooks through one."""
    pid = os.getppid()
    while True:
        stat = proc_stat(pid)
        if stat is None:
            raise SystemExit(f'isotty-idle: the process {pid} that ran the hook is gone')
        name, parent, start = stat
        if name not in SHELLS:
            return pid, start
        pid = parent


def state_path(agent, session):
    return os.path.join(STATE_DIR, f'{agent}-{session}.json')


def read_state(path):
    with open(path, encoding='utf-8') as f:
        return json.load(f)


def write_state(path, state):
    next_path = path + '.next'
    with open(next_path, 'w', encoding='utf-8') as f:
        json.dump(state, f)
    os.replace(next_path, path)


def cmd_hook(args):
    event = json.load(sys.stdin)
    name = event['hook_event_name']
    path = state_path(args.agent, event['session_id'])
    if name == 'SessionEnd':
        if os.path.exists(path):
            os.remove(path)
        return
    current = read_state(path)['state'] if os.path.exists(path) else None
    if name == 'UserPromptSubmit':
        state = WORKING
    elif name == 'Stop':
        # Claude Code tells what will wake the session again; Codex has
        # nothing that does.
        pending = event.get('background_tasks') or event.get('session_crons')
        state = WAITING if pending else IDLE
        # Codex reads a Stop hook's stdout as JSON.
        print('{}')
    elif name in ('Notification', 'Interrupt'):
        # A turn the human interrupted ends without Stop: Claude Code's
        # idle prompt, or Codex's Interrupt, says the session waits for input.
        if current != WORKING:
            return
        state = IDLE
    else:
        raise SystemExit(f'isotty-idle: unexpected hook event {name}')
    pid, start = agent_process()
    write_state(path, {'agent': args.agent, 'session': event['session_id'], 'state': state,
                       'pid': pid, 'pid_start': start, 'at': time.time()})


def alive(state):
    stat = proc_stat(state['pid'])
    return stat is not None and stat[2] == state['pid_start']


def boot_time():
    with open(KERNEL_STAT, encoding='utf-8') as f:
        for line in f:
            if line.startswith('btime '):
                return int(line.split()[1])
    raise SystemExit(f'isotty-idle: {KERNEL_STAT} has no btime')


def record(event):
    event['time'] = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())
    os.makedirs(os.path.dirname(EVENTS), exist_ok=True)
    with open(EVENTS, 'a', encoding='utf-8') as f:
        f.write(json.dumps(event, separators=(',', ':')) + '\n')


def cmd_check(_args):
    with open(CONFIG, encoding='utf-8') as f:
        seconds = json.load(f)['seconds']
    if seconds == 0:
        return
    now = time.time()
    # The last up counts as work: it writes the configuration.
    last = max(boot_time(), os.stat(CONFIG).st_mtime)
    working = False
    for name in os.listdir(STATE_DIR):
        if not name.endswith('.json'):
            continue
        path = os.path.join(STATE_DIR, name)
        state = read_state(path)
        if not alive(state):
            # The agent exited without SessionEnd, or the VM restarted.
            os.remove(path)
            continue
        if state['state'] in (WORKING, WAITING):
            working = True
        last = max(last, state['at'])
    if working:
        write_state(LAST_WORK, {'at': now})
        return
    if os.path.exists(LAST_WORK):
        last = max(last, read_state(LAST_WORK)['at'])
    if now - last < seconds:
        return
    reason = f'no agent work for {int(now - last) // 60} minutes'
    record({'type': 'idle-stop', 'result': reason})
    tell_opeco(f'VM powered off: {reason}')
    subprocess.run(['systemctl', 'poweroff'], check=True)


def tell_opeco(text):
    """Shows text on the card of the opeco session the VM keeps, as its
    owner, when it keeps one. The client records what it could not send and
    never fails, so the power-off goes on whatever happens here."""
    if not os.path.exists(OPECO_SESSION) or not os.path.isdir(OPECO_DIR):
        return
    owner = pwd.getpwuid(os.stat(OPECO_DIR).st_uid).pw_name
    try:
        subprocess.run(['sudo', '-u', owner, '-H', OPECO_SESSION, 'status', text], check=True, timeout=60)
    except (subprocess.SubprocessError, OSError) as e:
        # The power-off goes on; what could not be told is in the journal.
        print(f'isotty-idle: the opeco session was not told: {e}', file=sys.stderr)


def main():
    parser = argparse.ArgumentParser(prog='isotty-idle', description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command', required=True)
    hook = sub.add_parser('hook', help='the agents\' hook')
    hook.add_argument('agent', choices=['claude', 'codex'])
    hook.set_defaults(func=cmd_hook)
    sub.add_parser('check', help='power the VM off when no agent has had work for the configured time').set_defaults(func=cmd_check)
    args = parser.parse_args()
    args.func(args)


if __name__ == '__main__':
    main()
