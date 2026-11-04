# Browser Design

The VM has no display, but the human attached to it has a browser. IsoTTY
lets the agent open a URL there: `isotty-open <url>` in the VM asks the host
gateway, which runs the host desktop's opener. The shell `isotty attach`
opens has `BROWSER` set to the command, so `gh`, Python's `webbrowser`, a
click on a link in Codex and an agent's own "open in browser" reach the
host without being told. A file the agent wrote under the workspace is
shown the same way, as the host's synced copy.

```text
VM:   isotty-open https://...   (BROWSER=/usr/local/bin/isotty-open)
      isotty-open /workspace/figs/plot.png
        --POST /open--> 127.0.0.1:61001 ==ssh -R==> host gateway --> open / xdg-open
```

## What is opened

Any `http` or `https` URL. The tab appears in front of the human, who is
the one to judge it and the one who can close it; a URL that should not have
been opened is a mistake that ends there. Other schemes are refused, since
the host's opener would hand them to other applications (`file:`,
`vscode:`, `mailto:`). A refusal, like a host that is not attached, tells
the agent to put the URL in its report instead.

Every accepted open is recorded in the VM event log as `open-url`, so
`isotty audit logs` shows what the VM asked the human's browser to show.

## Files

`isotty-open <path>` with a path under the workspace shows the file in the
same browser. The workspace is synced to the host (see [SYNC.md](SYNC.md)),
so the file is on the human's machine already: the gateway flushes the sync
session, maps the path onto the project directory, and opens an `http` URL
under which the attach serves the host's copy. No server runs in the VM and
nothing leaves the machine, which is why the agent is told to show figures
and reports this way rather than through a service.

The file is served, not handed to the desktop opener: a `file:` URL would
be a double-click, and the VM chooses the file's name, content and mode,
so a `.command`, an app bundle or a script with its x bit set would run
instead of being shown. Served over `http`, the browser renders what it
can and offers the rest as a download, which is the posture every URL open
already has.

What the VM wrote is shown inert. Every response carries a
`Content-Security-Policy` of `sandbox`, so a script in an HTML or SVG file
does not run and the document has no origin to read the server with, and
of `default-src 'none'` with `img-src`, `style-src`, `font-src` and
`media-src` set to `'self'`, so a page loads nothing but what this server
holds and reaches no other site; `X-Content-Type-Options: nosniff` keeps a
file what its name says. No `Cross-Origin-Resource-Policy` is sent: the
sandbox leaves the page with an opaque origin, and the policy would count
the page's own images as foreign. The file server listens on a loopback port of its own
that is not forwarded into the VM, answers only requests addressed to that
port, and puts a random prefix in its paths; it lives as long as the
attach. The VM cannot open that server by `url`, only a file by its path.

The path must be under the workspace as the VM names it, and the file must
be a regular file under the project as the host finds it: a symlink that
leads out of the project is refused, because the VM can make one and the
sync carries it, and every request to the file server resolves the path
again and serves the file it checked. A file the sync keeps on the host
(see the ignores in [SYNC.md](SYNC.md): `.env`, keys, `.ssh/`, the audit
archives and the like) is not on the VM's side and is reported as not on
the host, so the VM learns nothing about it. No refusal names a host path.
`attach --target` has no project directory on the host, so it opens URLs
only. The event log records the file's real path as the VM names it,
never the server's URL.

## Setup

`isotty up` installs `/usr/local/bin/isotty-open` and the `isotty-open`
skill for each configured agent. `isotty attach` serves `/open` on the
gateway and starts the session shell with `BROWSER=/usr/local/bin/isotty-open`,
so the variable exists exactly when the opener works; `attach --no-host`
has no gateway, no opener and no `BROWSER`. In tmux the attach that holds
the gateway also sets `BROWSER` in the session environment, for the windows
opened later; a shell that was already running keeps what it had, and a
click there fails plainly while no gateway attach is alive.

[opeco](OPECO.md) pairs its device through the same path: the QR page it
serves in the VM is one more URL `isotty-open` opens on the host.
