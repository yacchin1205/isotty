# opeco Design

IsoTTY installs [opeco](https://github.com/yacchin1205/opeco-link) in the VM
by default whenever an agent is configured, so the agent can keep the person
informed on their phone or desktop while the host may be asleep.

## Why in the VM

The agent runs in the VM and keeps running when the person closes their
laptop. Notifications must therefore leave from the VM: opeco runs there and
talks to opeco.link directly. The host is needed only once, to pair a device.

## Pairing through the attach session

opeco shows a one-shot QR code on a loopback page of the machine it runs on
and opens it with the command named by `BROWSER`. In the VM there is no
browser, so `isotty attach` carries both the page and the "open it" request
to the host:

```text
VM:   opeco mcp --qr-port <port>, BROWSER=/usr/local/bin/isotty-open
        page on 127.0.0.1:<port>; session_create runs
        isotty-open http://127.0.0.1:<port>/qr/<token>
          --POST /open--> 127.0.0.1:61001 ==ssh -R==> host gateway
host: gateway accepts only URLs under http://127.0.0.1:<port>/ and runs the
      desktop opener; the browser reaches the page through
      ssh -L 127.0.0.1:<port>:127.0.0.1:<port> (added by attach)
```

The page is reached on the host at the address opeco serves it on in the VM,
so the URL opens unchanged and opeco reports `qr_opened: true` because
`isotty-open` succeeded. When the host is not attached, `isotty-open` fails,
opeco reports `qr_open_error`, and the agent shows the URL instead. After
pairing the host plays no part.

Every accepted open request is recorded in the VM event log (`open-url`), so
`isotty audit logs` shows what the VM asked the host's browser to show.

The QR forward appears as `opeco-qr` beside the project's own forwards and
exists only while attached. `attach --no-host` skips it together with the
other forwards and the gateway.

There is nothing to configure: opeco is installed whenever
`.isotty/agent.yaml` lists an agent.

## The QR port

opeco learns its QR port when the agent starts, and the host must use the same
number, so the port is fixed per project for the life of its VM. `up` picks
the lowest port from 61002 (above the Linux ephemeral range) that no other
project on this host holds and keeps it in the project state
(`~/.isotty/projects/<hash>.json`, which `down` removes); `attach` reads it
from there. Two projects attached from one host therefore never share the
port. Attaching the same project twice from one host still fails on the second
attach, because the forward cannot bind.

## What `up` installs

On every `isotty up`:

* the latest opeco release from GitHub Releases, verified against its
  `checksums.txt`, as `/usr/local/bin/opeco`
* `/usr/local/bin/isotty-open`, which posts a URL to the host gateway
* for Claude: `opeco-link` in `~/.claude.json` as an MCP server running
  `opeco --qr-port <port> mcp` with `BROWSER=/usr/local/bin/isotty-open`,
  opeco's agent hooks merged into `~/.claude/settings.json`, and opeco's
  skill under `~/.claude/skills/`
* for Codex: the same server and environment in `~/.codex/config.toml`
  between marker comments, and opeco's hooks merged into `~/.codex/hooks.json`

The hooks and the skill are fetched from the opeco repository at the same
release, so opeco stays the only place that defines them.

IsoTTY follows opeco's latest release rather than pinning one: opeco is a
client of the opeco.link service and its apps, and the latest release is the
one opeco keeps compatible with them. What IsoTTY requires of the CLI is
expressed as a minimum version in `internal/isotty/opeco/setup.go`; `up`
fails when the latest release is older than that.

## Security Posture

* opeco's session keys live in the VM process, like everything else the
  agent does there; the host never holds them
* the QR page binds to the VM loopback and is reachable from the host only
  through the attach session's forward
* the VM can make the host browser show only URLs under the forwarded QR
  port; any other URL is refused by the gateway, and every accepted request
  is audited
* what that port serves is under the VM's control: the gateway cannot tell
  opeco's page from anything else bound to it, so the host browser renders
  VM-controlled content when it opens the page
* treat an unused pairing QR code or URL as a temporary secret, as
  [opeco's security notes](https://github.com/yacchin1205/opeco-link#security-notes)
  describe
