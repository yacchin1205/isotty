#!/usr/bin/env python3
"""Pull and push images of pinned repositories through the IsoTTY gateway.

The gateway runs on the host inside `isotty attach`. The host's Docker
talks to the registry, with the host's login and from wherever the host can
reach, and the image travels between it and the Docker of this VM. No
registry credential enters this VM. The repositories are the ones pinned in
.isotty/docker.yaml, each for pulling (read) or for pushing too (write).

An image travels as blobs, its config and its layers, and each side keeps
the blobs it received for a while, so that a pull run again or a push of an
image that changed a little moves only what is new.
"""
import argparse
import hashlib
import http.client
import io
import json
import os
import re
import stat
import subprocess
import sys
import tarfile
import tempfile
import time

GATEWAY_PORT = __ISOTTY_GATEWAY_PORT__

UNREACHABLE = (
    'isotty-docker: the Docker gateway at 127.0.0.1:{port} is not reachable ({reason}). '
    'It is available only while the host is attached with `isotty attach`. '
    'Retry after re-attach.'
)

NOT_PULLED = (
    'isotty-docker: the host went away while its Docker was pulling; nothing was loaded. '
    'Retry the pull after re-attach.'
)

BROKE_OFF = (
    'isotty-docker: the image did not arrive whole; nothing was loaded. '
    'Retry the pull, which fetches what is missing.'
)

NOT_SENT = (
    'isotty-docker: the host went away while the image was being sent; nothing was pushed. '
    'Retry the push after re-attach.'
)

WENT_AWAY = (
    'isotty-docker: the host went away while its Docker was pushing, so whether the registry has '
    'the image is not known. Retry the push after re-attach.'
)

BLOB = re.compile(r'blobs/sha256/([0-9a-f]{64})')


def request(method, path, payload=None, blob=None, size=None, cut=BROKE_OFF):
    """Ask the gateway; a blob is sent as it is, anything else as JSON."""
    conn = http.client.HTTPConnection('127.0.0.1', GATEWAY_PORT)
    if blob is None:
        body = None if payload is None else json.dumps(payload)
        headers = {'Content-Type': 'application/json'}
    else:
        body = blob
        headers = {'Content-Type': 'application/octet-stream', 'Content-Length': str(size)}
    try:
        conn.request(method, '/docker' + path, body=body, headers=headers)
        res = conn.getresponse()
    except http.client.RemoteDisconnected:
        # The gateway took the request and cut the connection: the host
        # could not finish what it had begun.
        raise SystemExit(cut)
    except OSError as e:
        raise SystemExit(UNREACHABLE.format(port=GATEWAY_PORT, reason=e))
    if res.status != 200:
        # The gateway always answers errors as {"error": ...}.
        raise SystemExit('isotty-docker: ' + json.load(res)['error'])
    return res


def cmd_status(args):
    repos = json.load(request('GET', '/status'))['repos']
    print('Images of these repositories are available through the isotty gateway:')
    for repo in sorted(repos):
        print(f'  {repo}: {repos[repo]}')


def vm_platform():
    """The platform of this VM's Docker, which images are pulled and pushed for."""
    try:
        return subprocess.run(
            ['docker', 'version', '--format', '{{.Server.Os}}/{{.Server.Arch}}'],
            check=True, stdout=subprocess.PIPE, text=True,
        ).stdout.strip()
    except FileNotFoundError:
        raise SystemExit(
            'isotty-docker: this VM has no docker command, and images travel between its Docker '
            "and the host's. The human enables Docker for the project on the host with "
            '`isotty runtime service enable docker` and runs `isotty up`.'
        )
    except subprocess.CalledProcessError:
        # docker has said why on stderr.
        raise SystemExit("isotty-docker: this VM's Docker did not answer.")


def cache_dir():
    """The directory holding received blobs, swept of what has not been used
    for as long as the host keeps things. Only this user may enter it: the
    temporary directory is shared, and in a directory someone else made they
    could swap a blob after its digest was checked."""
    keep = json.load(request('GET', '/status'))['keep']
    path = os.path.join(tempfile.gettempdir(), f'isotty-docker-{os.getuid()}')
    os.makedirs(path, mode=0o700, exist_ok=True)
    info = os.lstat(path)
    if not stat.S_ISDIR(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o700 or info.st_uid != os.getuid():
        raise SystemExit(f'isotty-docker: {path} is not a directory that only this user can enter; remove it')
    now = time.time()
    for name in os.listdir(path):
        try:
            entry = os.path.join(path, name)
            if now - os.stat(entry).st_mtime > keep:
                os.remove(entry)
        except FileNotFoundError:
            # A transfer finished meanwhile and the blob took its own name.
            pass
    return path


def blob_path(cache, digest):
    return os.path.join(cache, digest[len('sha256:'):])


def have(cache, digest):
    """Whether the blob is held, which counts as a use of it."""
    try:
        os.utime(blob_path(cache, digest))
        return True
    except FileNotFoundError:
        return False


def keep(cache, digest, source, broken):
    """Keep what source yields under the digest. What does not hash to it,
    or breaks off, is not kept, and the command ends with broken."""
    fd, incoming = tempfile.mkstemp(prefix='incoming-', dir=cache)
    hashed = hashlib.sha256()
    try:
        with os.fdopen(fd, 'wb') as out:
            while True:
                chunk = source.read(1 << 20)
                if not chunk:
                    break
                out.write(chunk)
                hashed.update(chunk)
        whole = 'sha256:' + hashed.hexdigest() == digest
    except (http.client.HTTPException, OSError):
        whole = False
    if not whole:
        os.remove(incoming)
        raise SystemExit(broken)
    os.replace(incoming, blob_path(cache, digest))


def cmd_pull(args):
    platform = vm_platform()
    cache = cache_dir()
    image = json.load(request('POST', '/pull', {'ref': args.ref, 'platform': platform}, cut=NOT_PULLED))
    if image.get('pulled'):
        print(f"The host pulled from the registry: {image['pulled']}", file=sys.stderr)
    blobs = list(dict.fromkeys([image['config']] + image['layers']))
    missing = [digest for digest in blobs if not have(cache, digest)]
    print(
        f'Receiving {len(missing)} of {len(blobs)} blobs; what arrived in an earlier pull is not fetched again.',
        file=sys.stderr,
    )
    for digest in missing:
        keep(cache, digest, request('GET', '/blobs/' + digest), BROKE_OFF)

    # The archive Docker loads is written here, from the blobs, under the
    # name the pull was asked for.
    def name(digest):
        return 'blobs/sha256/' + digest[len('sha256:'):]

    load = subprocess.Popen(['docker', 'load'], stdin=subprocess.PIPE)
    try:
        with tarfile.open(fileobj=load.stdin, mode='w|') as tar:
            manifest = json.dumps([{
                'Config': name(image['config']),
                'RepoTags': [args.ref],
                'Layers': [name(digest) for digest in image['layers']],
            }]).encode()
            entry = tarfile.TarInfo('manifest.json')
            entry.size = len(manifest)
            tar.addfile(entry, io.BytesIO(manifest))
            for digest in blobs:
                tar.add(blob_path(cache, digest), arcname=name(digest))
        load.stdin.close()
    except BrokenPipeError:
        # docker load stopped reading and has said why on stderr.
        pass
    if load.wait() != 0:
        raise SystemExit('isotty-docker: docker load failed')


def cmd_push(args):
    platform = vm_platform()
    cache = cache_dir()
    with tempfile.TemporaryDirectory(prefix='isotty-docker-') as scratch:
        archive = os.path.join(scratch, 'image.tar')
        if subprocess.run(['docker', 'save', '--platform', platform, '--output', archive, args.ref]).returncode != 0:
            # docker has said why on stderr.
            raise SystemExit(f"isotty-docker: this VM's Docker could not save {args.ref} for {platform}")
        with tarfile.open(archive) as tar:
            images = json.load(tar.extractfile('manifest.json'))
            if len(images) != 1:
                raise SystemExit(f'isotty-docker: the archive of {args.ref} holds {len(images)} images, not one')
            names = [images[0]['Config']] + images[0]['Layers']
            for name in names:
                if not BLOB.fullmatch(name):
                    raise SystemExit(
                        f"isotty-docker: this VM's Docker saved {args.ref} with {name}, not as blobs named "
                        'by their digests; Docker 25 or newer does'
                    )
            digests = ['sha256:' + BLOB.fullmatch(name).group(1) for name in names]
            # The host says here whether the repository may be pushed to,
            # before anything is sent.
            missing = json.load(request('POST', '/blobs/missing', {'ref': args.ref, 'digests': digests}, cut=NOT_SENT))['missing']
            # The blobs go into this VM's cache, where a pull of the pushed
            # image finds them, and are sent from there.
            for name, digest in zip(names, digests):
                if not have(cache, digest):
                    keep(cache, digest, tar.extractfile(name),
                         f"isotty-docker: this VM's Docker saved {name} with content that does not hash to it")
    sizes = {digest: os.path.getsize(blob_path(cache, digest)) for digest in missing}
    print(
        f'Sending {len(missing)} of {len(set(digests))} blobs ({sum(sizes.values()) / 1e6:.1f} MB); '
        'what the host holds from an earlier push is not sent again.',
        file=sys.stderr,
    )
    for digest in missing:
        with open(blob_path(cache, digest), 'rb') as blob:
            request('PUT', '/blobs/' + digest, blob=blob, size=sizes[digest], cut=NOT_SENT)
    print("Pushing from the host's Docker.", file=sys.stderr)
    pushed = json.load(request('POST', '/push', {'ref': args.ref, 'config': digests[0], 'layers': digests[1:]}, cut=WENT_AWAY))
    print(f"Pushed {pushed['ref']} as {pushed['digest']}")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command_name', required=True)
    p = sub.add_parser('status', help='check that the gateway is reachable and list the pinned repositories')
    p.set_defaults(func=cmd_status)
    p = sub.add_parser('pull', help='pull an image of a pinned repository into the Docker of this VM')
    p.add_argument('ref', help='registry/path:tag, with the registry spelled out, as in docker.io/library/alpine:3.20')
    p.set_defaults(func=cmd_pull)
    p = sub.add_parser('push', help="push an image of this VM's Docker to a repository pinned for it")
    p.add_argument('ref', help='registry/path:tag, with the registry spelled out; the image has this name in the Docker of this VM')
    p.set_defaults(func=cmd_push)
    args = parser.parse_args()
    args.func(args)


if __name__ == '__main__':
    main()
