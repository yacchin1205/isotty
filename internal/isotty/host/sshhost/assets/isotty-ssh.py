#!/usr/bin/env python3
"""Reach the machines on the host's network that the human named, through
the IsoTTY gateway.

`ssh <name>` runs `isotty-ssh connect <name>` as its ProxyCommand: the
gateway, which runs in the host's isotty daemon, opens a TCP
connection to the machine and this command carries the SSH session over it
on its standard input and output. `isotty-ssh list` names the machines.
"""
import json
import socket
import sys
import threading

GATEWAY = ('127.0.0.1', __ISOTTY_GATEWAY_PORT__)
CONFIG = '/etc/ssh/ssh_config.d/isotty-hosts.conf'


def fail(message):
    sys.stderr.write('isotty-ssh: ' + message + '\n')
    sys.exit(255)


def read_response(sock):
    """The gateway's answer to the upgrade, and the bytes after it."""
    data = b''
    while b'\r\n\r\n' not in data:
        chunk = sock.recv(4096)
        if not chunk:
            fail('the gateway closed the connection without answering')
        data += chunk
    head, rest = data.split(b'\r\n\r\n', 1)
    return head.decode('latin-1'), rest


def connect(name):
    try:
        sock = socket.create_connection(GATEWAY, timeout=30)
    except OSError as e:
        fail(f'the gateway at {GATEWAY[0]}:{GATEWAY[1]} is not reachable ({e}); '
             'the host is asleep or offline, or its isotty daemon is not running; tell the human')
    sock.sendall((f'GET /ssh/{name} HTTP/1.1\r\nHost: {GATEWAY[0]}:{GATEWAY[1]}\r\n'
                  'Connection: Upgrade\r\nUpgrade: isotty-ssh\r\n\r\n').encode())
    head, rest = read_response(sock)
    status = head.split('\r\n', 1)[0]
    if ' 101 ' not in status + ' ':
        length = 0
        for line in head.split('\r\n')[1:]:
            key, _, value = line.partition(':')
            if key.strip().lower() == 'content-length':
                length = int(value.strip())
        body = rest
        while len(body) < length:
            chunk = sock.recv(4096)
            if not chunk:
                break
            body += chunk
        try:
            message = json.loads(body)['error']
        except (ValueError, KeyError, TypeError):
            message = body.decode('utf-8', 'replace').strip() or status
        fail(message)
    sock.settimeout(None)
    out = sys.stdout.buffer
    if rest:
        out.write(rest)
        out.flush()

    def to_machine():
        stdin = sys.stdin.buffer.raw
        while True:
            chunk = stdin.read(65536)
            if not chunk:
                break
            sock.sendall(chunk)
        try:
            sock.shutdown(socket.SHUT_WR)
        except OSError:
            pass

    threading.Thread(target=to_machine, daemon=True).start()
    while True:
        chunk = sock.recv(65536)
        if not chunk:
            break
        out.write(chunk)
        out.flush()


def names():
    try:
        with open(CONFIG, encoding='utf-8') as f:
            return [line.split()[1] for line in f if line.startswith('Host ')]
    except FileNotFoundError:
        return []


def main():
    args = sys.argv[1:]
    if len(args) == 2 and args[0] == 'connect':
        connect(args[1])
        return
    if args == ['list']:
        found = names()
        if not found:
            print('No machine is configured; the human adds one with `isotty host ssh add` on the host.', file=sys.stderr)
        for name in found:
            print(name)
        return
    raise SystemExit('usage: isotty-ssh list\n       isotty-ssh connect NAME   (the ProxyCommand of `ssh NAME`)')


if __name__ == '__main__':
    main()
