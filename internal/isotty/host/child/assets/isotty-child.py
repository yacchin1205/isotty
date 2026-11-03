#!/usr/bin/env python3
"""Create and use child VMs through the IsoTTY gateway.

The gateway runs on the host inside `isotty attach` and creates VMs next to
this one with the human's gcloud login, within what is pinned in
.isotty/children.yaml. A child is reachable over SSH from this VM only, with
a key that exists only here, and is deleted when this VM stops or goes down.
"""
import argparse
import getpass
import json
import os
import subprocess
import sys
import urllib.error
import urllib.request

GATEWAY = 'http://127.0.0.1:__ISOTTY_GATEWAY_PORT__/child'
KEY_DIR = os.path.expanduser('~/.ssh/isotty-child')

UNREACHABLE = (
    'isotty-child: the child VM gateway at {gateway} is not reachable ({reason}). '
    'It is available only while the host is attached with `isotty attach`. '
    'Retry after re-attach.'
)


def call(method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(GATEWAY + path, data=data, method=method, headers={'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(req, timeout=600) as res:
            return json.load(res)
    except urllib.error.HTTPError as e:
        raise SystemExit('isotty-child: ' + json.load(e)['error'])
    except urllib.error.URLError as e:
        raise SystemExit(UNREACHABLE.format(gateway=GATEWAY, reason=e.reason))


def key_path(name):
    return os.path.join(KEY_DIR, name)


def info_path(name):
    return os.path.join(KEY_DIR, name + '.json')


def known_hosts():
    return os.path.join(KEY_DIR, 'known_hosts')


def load_info(name):
    try:
        with open(info_path(name), encoding='utf-8') as f:
            return json.load(f)
    except FileNotFoundError:
        raise SystemExit(f'isotty-child: no key for {name} here; `isotty-child list` shows the children, `create` makes one')


def print_children(children):
    for child in children:
        ports = ','.join(str(p) for p in child['Expose']) or '-'
        print(f"{child['Name']}\t{child['Status']}\t{child['MachineType']}\t{child['InternalIP']}\t{child['ExternalIP'] or '-'}\t{ports}")


def cmd_status(args):
    status = call('GET', '/status')
    agreement = status['agreement']
    print(f"Child VMs are created through the isotty gateway next to {status['parent']}. This project may have:")
    print(f"  up to {agreement['max']} child VM(s) of type {', '.join(agreement['machine_types'])}")
    print(f"  boot disk up to {agreement['max_boot_disk_size']} (default {status['defaults']['BootDiskSize']})")
    print(f"  ports open to the world: {', '.join(str(p) for p in agreement['expose']) or 'none'}")
    print(f"  default image: {status['defaults']['ImageProject']}/{status['defaults']['ImageFamily']}")


def cmd_list(args):
    print_children(call('GET', '/list')['children'])


def cmd_create(args):
    body = {
        'name': args.name, 'user': getpass.getuser(), 'machine_type': args.machine_type,
        'boot_disk_size': args.boot_disk_size or '', 'image_family': args.image_family or '',
        'image_project': args.image_project or '', 'expose': args.expose,
    }
    answer = call('POST', '/create', body)
    child = answer['child']
    os.makedirs(KEY_DIR, mode=0o700, exist_ok=True)
    with open(key_path(args.name), 'w', encoding='utf-8', opener=lambda p, f: os.open(p, f, 0o600)) as f:
        f.write(answer['private_key'])
    with open(info_path(args.name), 'w', encoding='utf-8') as f:
        json.dump({'user': answer['user'], 'internal_ip': child['InternalIP'], 'external_ip': child['ExternalIP'], 'instance': child['Instance']}, f)
    print(f"# {child['Instance']} {child['Status']}; ssh with `isotty-child ssh {args.name}`", file=sys.stderr)
    print_children([child])


def cmd_ssh(args):
    info = load_info(args.name)
    # The host key is filed under the instance name: an internal IP is
    # reused by the next child, whose key differs.
    command = [
        'ssh', '-i', key_path(args.name),
        '-o', 'IdentitiesOnly=yes',
        '-o', 'StrictHostKeyChecking=accept-new',
        '-o', 'UserKnownHostsFile=' + known_hosts(),
        '-o', 'HostKeyAlias=' + info['instance'],
        f"{info['user']}@{info['internal_ip']}",
    ] + args.command
    os.execvp('ssh', command)


def cmd_power(args):
    answer = call('POST', '/' + args.command_name, {'name': args.name})
    child = answer['child']
    if os.path.exists(info_path(args.name)):
        info = load_info(args.name)
        info.update({'internal_ip': child['InternalIP'], 'external_ip': child['ExternalIP']})
        with open(info_path(args.name), 'w', encoding='utf-8') as f:
            json.dump(info, f)
    print(f"# {child['Instance']} {child['Status']}", file=sys.stderr)
    print_children([child])


def cmd_delete(args):
    answer = call('POST', '/delete', {'name': args.name})
    for path in (key_path(args.name), info_path(args.name)):
        try:
            os.remove(path)
        except FileNotFoundError:
            pass
    if os.path.exists(known_hosts()):
        subprocess.run(['ssh-keygen', '-q', '-R', answer['instance'], '-f', known_hosts()], check=False, capture_output=True)
    print(f"# deleted {answer['instance']}", file=sys.stderr)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command_name', required=True)
    p = sub.add_parser('status', help='check that the gateway is reachable and show what this project may create')
    p.set_defaults(func=cmd_status)
    p = sub.add_parser('list', help='show the child VMs: name, status, machine type, internal IP, external IP, open ports')
    p.set_defaults(func=cmd_list)
    p = sub.add_parser('create', help='create a child VM and keep its SSH key here')
    p.add_argument('name', help='short name, for example web')
    p.add_argument('--machine-type', required=True, help='one of the types `status` lists')
    p.add_argument('--boot-disk-size', help='for example 100GB; the default is what `status` shows')
    p.add_argument('--image-family')
    p.add_argument('--image-project')
    p.add_argument('--expose', type=int, action='append', default=[], help='port to open to the world (repeatable); only the ports `status` lists')
    p.set_defaults(func=cmd_create)
    p = sub.add_parser('ssh', help='open a shell on a child, or run a command there: isotty-child ssh <name> -- <command>')
    p.add_argument('name')
    p.add_argument('command', nargs=argparse.REMAINDER)
    p.set_defaults(func=cmd_ssh)
    p = sub.add_parser('stop', help='stop a child VM; it keeps its disk and key, and start brings it back')
    p.add_argument('name')
    p.set_defaults(func=cmd_power)
    p = sub.add_parser('start', help='start a stopped child VM; it gets a new external IP, so set its DNS records again')
    p.add_argument('name')
    p.set_defaults(func=cmd_power)
    p = sub.add_parser('delete', help='delete a child VM and its key')
    p.add_argument('name')
    p.set_defaults(func=cmd_delete)
    args = parser.parse_args()
    if getattr(args, 'command', None) and args.command[0] == '--':
        args.command = args.command[1:]
    args.func(args)


if __name__ == '__main__':
    main()
