#!/usr/bin/env python3
"""Ask the host to open a URL, or a file of this workspace, for the human.

http and https URLs open in the human's browser. A path under the workspace
opens the host's synced copy of that file, with the application for its
type. The gateway exists while the host is attached. Exit status and stderr
carry the reason when nothing opened.
"""
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

GATEWAY = 'http://127.0.0.1:__ISOTTY_GATEWAY_PORT__'


def main():
    if len(sys.argv) != 2:
        raise SystemExit('usage: isotty-open URL|PATH')
    target = sys.argv[1]
    if urllib.parse.urlsplit(target).scheme.lower() in ('http', 'https'):
        body = {'url': target}
    else:
        body = {'path': os.path.abspath(target)}
    req = urllib.request.Request(GATEWAY + '/open', data=json.dumps(body).encode(),
                                 headers={'content-type': 'application/json'}, method='POST')
    try:
        with urllib.request.urlopen(req, timeout=60):
            return
    except urllib.error.HTTPError as e:
        raise SystemExit('isotty-open: ' + json.load(e)['error'])
    except urllib.error.URLError as e:
        raise SystemExit(f'isotty-open: the host is not attached ({e.reason}); give the URL or path to the human instead')


if __name__ == '__main__':
    main()
