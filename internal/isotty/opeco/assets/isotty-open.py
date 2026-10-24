#!/usr/bin/env python3
"""Ask the host to open a URL in its browser through the isotty gateway.

Only the opeco QR page is accepted; the gateway exists while the host is
attached. Exit status and stderr carry the reason when nothing opened.
"""
import json
import sys
import urllib.error
import urllib.request

GATEWAY = 'http://127.0.0.1:__ISOTTY_GATEWAY_PORT__'


def main():
    if len(sys.argv) != 2:
        raise SystemExit('usage: isotty-open URL')
    req = urllib.request.Request(GATEWAY + '/open', data=json.dumps({'url': sys.argv[1]}).encode(),
                                 headers={'content-type': 'application/json'}, method='POST')
    try:
        with urllib.request.urlopen(req, timeout=30):
            return
    except urllib.error.HTTPError as e:
        raise SystemExit('isotty-open: ' + json.load(e)['error'])
    except urllib.error.URLError as e:
        raise SystemExit(f'isotty-open: the host is not attached ({e.reason}); open the URL on the host by hand')


if __name__ == '__main__':
    main()
