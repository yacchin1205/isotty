# Browser Design

The VM has no display, but the human attached to it has a browser. IsoTTY
lets the agent open a URL there: `isotty-open <url>` in the VM asks the host
gateway, which runs the host desktop's opener. The shell `isotty attach`
opens has `BROWSER` set to the command, so `gh`, Python's `webbrowser`, a
click on a link in Codex and an agent's own "open in browser" reach the
host without being told.

```text
VM:   isotty-open https://...   (BROWSER=/usr/local/bin/isotty-open)
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

## Setup

`isotty up` installs `/usr/local/bin/isotty-open` and the `isotty-open`
skill for each configured agent. `isotty attach` serves `/open` on the
gateway and starts the session shell with `BROWSER=/usr/local/bin/isotty-open`,
so the variable exists exactly when the opener works; `attach --no-host`
has no gateway, no opener and no `BROWSER`.

[opeco](OPECO.md) pairs its device through the same path: the QR page it
serves in the VM is one more URL `isotty-open` opens on the host.
