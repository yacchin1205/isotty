# Browser Design

The VM has no display, but the human attached to it has a browser. IsoTTY
lets the agent open a URL there: `isotty-open <url>` in the VM asks the host
gateway, which runs the host desktop's opener. The shell `isotty attach`
opens has `BROWSER` set to the command, so `gh`, Python's `webbrowser`
and an agent's own "open in browser" reach the host without being told.
A file the agent wrote under the workspace is shown the same way, as the
host's synced copy, in a viewer where the human can comment on it for the
agent to read. A link an agent prints is the terminal's business:
Codex marks a URL up as an OSC 8 hyperlink, the human's terminal opens it
on a click, and the VM's tmux relays it (see [VM.md](VM.md)).

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

What opens is the viewer, a page of the file server's own, with the file
in it and a place for the human to comment on it (next section). The file
is put on the page as what it is: a Markdown file converted on the host,
since that is what a report the agent writes most often is, with raw HTML
in the source left out and nothing that needs a script, such as a diagram
or formula block, rendered; an image as an image; any other text, HTML
included, line by line as text; what is none of those, a PDF or a binary,
is not put on the page but linked, for the browser to show as it can. A
path a Markdown file names leads to the neighbour under the same prefix,
resolved and checked like any other: an image to the file itself, a link
to its page.

What the VM wrote is shown inert. The viewer is the server's code, so its
own script runs, under a `Content-Security-Policy` of `default-src 'none'`
with `script-src`, `style-src`, `img-src`, `connect-src`, `font-src` and
`media-src` set to `'self'`: it loads nothing but what this server holds
and reaches no other site, and no inline script runs. What the VM wrote
enters that page only as text nodes, as an `<img>`, or as the converter's
output. The file itself, under `raw/`, carries a policy of `sandbox`, so a
script in an HTML or SVG file does not run and the document has no origin
to read the server with, and of `default-src 'none'` with the same
`'self'` sources; `X-Content-Type-Options: nosniff` keeps a file what its
name says. No `Cross-Origin-Resource-Policy` is sent: the sandbox leaves
such a page with an opaque origin, and the policy would count the page's
own images as foreign. The file server listens on a loopback port of its
own that is not forwarded into the VM, answers only requests addressed to
that port, and puts a random prefix in its paths; it lives as long as the
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

## Review

Telling an agent where in a file something is wrong is tedious in words;
in the viewer the human points at it. A line or a range of a text, a
paragraph, heading or list item of a Markdown page (each carries the
source lines it came from), a spot or an area of an image, or the file as
a whole takes a comment, which the human can later edit, mark resolved or
delete.

The comments are kept beside the project, one file per reviewed file:

```text
<project>/.isotty/reviews/<path>.jsonl
```

```json
{"id":"1d07…","at":"2026-11-08T13:52:59Z","anchor":{"lines":[2,3],"quote":"line <two>"},"body":"…","file_sha256":"sha256:…","resolved":false}
```

The anchor is `lines` with the text at the first of them quoted, `point`
or `rect` in fractions of the image's size, or empty for the whole file.
It names the place as the file was then; `file_sha256` says whether the
file has changed since, and the quote finds the place again when it has.

`.isotty/` is synced, so the comments reach the VM as files and no channel
of their own is needed; they reach it after a detach too, with the next
sync. `isotty-open reviews [--all] [<path>]` in the VM lists them by file
and place, says when the file changed since and where the quoted text is
now, and leaves resolved ones out unless asked. The host writes these
files, through the viewer; the agent's skill tells it to read them and
leave them alone, so that the two never edit the same file and the sync
has nothing to reconcile. The directory is in the workspace, so this is
an instruction to the agent, not something the host enforces: an agent
that edits a file there edits what the viewer shows and what it reads
back as the human's words. Whether the directory is committed is the
project's business, as with the rest of `.isotty/`.

The viewer talks to the file server under its token (`reviews/<path>`),
which pages of other sites in that browser do not know, and only about
files the server would show, located the same way. The comments file is
reached without following a symlink: the host made every directory on the
way to it, so a link there is the VM's, and a path through one is refused
rather than written somewhere else. The comments are the
human's words and are not in the audit log, which records what the VM did.

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
