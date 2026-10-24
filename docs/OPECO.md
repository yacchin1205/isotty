# opeco Design

IsoTTY installs [opeco](https://github.com/yacchin1205/opeco-link) in the VM
by default whenever an agent is configured, so the agent can keep the person
informed on their phone or desktop while the host may be asleep.

## Why in the VM

The agent runs in the VM and keeps running when the person closes their
laptop. Notifications must therefore leave from the VM: opeco runs there and
talks to opeco.link directly. The host is needed only once, to pair a device.

## Pairing through the attach session

opeco shows a one-shot QR code on a loopback page of the machine it runs on.
In the VM there is no browser, so `isotty attach` carries that page to the
host:

```text
VM:   opeco mcp --no-browser --qr-port 61002   (page on 127.0.0.1:61002)
      session_create → qr_image_url = http://127.0.0.1:61002/qr/<token>
host: ssh -L 127.0.0.1:61002:127.0.0.1:61002    (added by attach)
```

The same port on both ends means the URL opeco prints opens unchanged on
the host. The agent shows the URL; the person opens it and scans the code.
After that the host plays no part.

The forward appears as `opeco-qr` beside the project's own forwards and
exists only while attached. `attach --no-forward` skips it like the others.

There is nothing to configure: opeco is installed whenever
`.isotty/agent.yaml` lists an agent, and the QR port is fixed at 61002, above
the Linux ephemeral range.

## What `up` installs

On every `isotty up`:

* the pinned opeco release from GitHub Releases, verified against its
  `checksums.txt`, as `/usr/local/bin/opeco`
* for Claude: `opeco-link` in `~/.claude.json` as an MCP server running
  `opeco --no-browser --qr-port <port> mcp`, opeco's agent hooks merged into
  `~/.claude/settings.json`, and opeco's skill under `~/.claude/skills/`
* for Codex: the same server in `~/.codex/config.toml` between marker
  comments, and opeco's hooks merged into `~/.codex/hooks.json`

The hooks and the skill are fetched from the opeco repository at the pinned
release, so opeco stays the only place that defines them. The release is
pinned in `internal/isotty/opeco/setup.go`; bumping it is a code change.

## Security Posture

* opeco's session keys live in the VM process, like everything else the
  agent does there; the host never holds them
* the QR page binds to the VM loopback and is reachable from the host only
  through the attach session's forward
* an unused pairing QR code or URL is a temporary secret, as opeco's own
  documentation says; the page expires after ten minutes
