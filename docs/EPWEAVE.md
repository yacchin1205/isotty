# ep_weave Design

IsoTTY can let the agent in the VM share results with other projects through
[ep_weave](https://github.com/NII-cloud-operation/ep_weave), the
Etherpad-based note hub running on the host, without exposing ep_weave itself
to the VM.

## Problem

A local ep_weave has no authentication on pads: anyone who can reach its port
can read, edit and delete every pad through the editor protocol. Forwarding
that port into the VM would put the whole note hub, including other projects'
results and the human's own notes, inside the VM's trust domain. That defeats
the point of IsoTTY.

## Model

```text
agent in VM --isotty-notes--> 127.0.0.1:61001/epweave ==ssh -R==> host gateway in `isotty attach` --> ep_weave
```

* The host gateway is an HTTP server inside the `isotty attach` process,
  bound to a host loopback port and exposed on the VM's loopback at port
  61001 with `ssh -R`. ep_weave is one of its services, mounted under
  `/epweave`.
* Only the gateway holds the ep_weave credentials. The VM sees an HTTP
  endpoint and nothing else.
* The service offers a fixed, narrow operation set. Delete and whole-pad
  rewrite do not exist in it.

## Config

Host credentials, never synced:

```text
~/.isotty/epweave.yaml
```

```yaml
url: http://localhost:9001
client_id: isotty
client_secret: ...
```

The client must be registered in ep_weave's `settings.json` under
`sso.clients` with `grant_types: ["client_credentials"]` and
`extraParams: [{"name": "admin", "value": "true"}]`.

Project config, synced with the rest of `.isotty/`:

```text
./.isotty/epweave.yaml
```

```yaml
prefix: isotty
```

```bash
isotty host epweave enable --prefix isotty
isotty host epweave show
isotty host epweave disable
```

### Pin

`.isotty/` is synced two-way with `/workspace`, so an agent in the VM could
rewrite `epweave.yaml` and widen its own prefix. `isotty host epweave enable`
therefore also records the accepted values on the host:

```text
~/.isotty/projects/<project-hash>.epweave.yaml
```

`up`, `attach` and `attach --target` compare the synced file with the pin
and refuse to continue when they differ or when no pin exists. Accepting a
change means running `isotty host epweave enable` on the host again.

## Operations

| operation | prefix check | Etherpad calls |
|---|---|---|
| search(query) | no | `/ep_weave/api/search` |
| read(title) | no | getRevisionsCount, getText / getHTML |
| create(title, markdown, tags) | yes | createPad, setHTML |
| append(title, markdown) | yes | getHTML, setHTML |
| changes(title, since) | no | getText at both revisions, createDiffHTML, getAuthorName |

Writes are allowed only for titles under `<prefix>/`. Reads and search cover
every pad: sharing results between projects is the purpose. Titles are
resolved through the search index; `GET /t/<title>` is never used because it
creates missing pads. The index is updated asynchronously after a write, so a
lookup retries for up to ten seconds.

Writes return the pad's new revision so the client can confirm the change
landed. `changes` returns the lines added between two revisions and the
authors involved; the full diff HTML is available on request only, because
it runs to tens of kilobytes.

`append` inserts above the trailing hashtag line so tags stay last. Markdown
support is a small subset on purpose: `#`/`##`/`###` headings, `-` bullets,
paragraphs.

## MCP relay

ep_weave can run [ep_ai_mcp](https://github.com/ether/ep_ai_mcp), which
exposes pad text, authorship and editing as MCP tools at `/mcp`. The gateway
relays it at `/epweave/mcp`, and `isotty up` registers that URL as the
`epweave` MCP server of each configured agent (Claude in `~/.claude.json`,
Codex in `~/.codex/config.toml`).

The relay adds the host token and passes everything through except the
three tools that change a pad: `edit_pad`, `create_pad` and
`send_chat_message`. For those it

* sets `authorMapper` and `authorName` to the project's author, whatever
  the VM sent, so the writes carry the same author as the `isotty-notes`
  operations;
* finds the pad's title (the first line of its text; for `create_pad` the
  first line of the new text) and refuses, as a tool error the agent sees,
  a title outside the prefix before ep_weave sees the call;
* gives a new pad a UUID id like the ones ep_weave creates;
* records the write in the VM event log as `epweave-<tool>`.

`edit_pad` uses `makeSplice`, so a human's lines keep their author. It
writes plain text only; the Markdown subset stays with `isotty-notes`.

The ep_weave to run is the `isotty` branch of
https://github.com/yacchin1205/ep_weave. Its image bundles ep_ai_core and
ep_ai_mcp, the latter from the fork
https://github.com/yacchin1205/ep_ai_mcp (branch `isotty`): the upstream
1.0.2 does not hand Etherpad's already parsed request body to the MCP
transport, so `/mcp` never answers, and it makes a new author on every
call; the fork fixes the first and adds `authorMapper` for the second. The
branch also authenticates `/ep_weave/api/search` like the Etherpad API.

## Attribution

Writes are attributed to an Etherpad author per project, mapped from the
project hash and named `isotty:<prefix>`. Humans can tell which project's
agent wrote what by colour and in the timeslider.

## Audit

Every create and append is recorded in the VM event log
(`/var/log/isotty/events.jsonl`) with the title and pad id, so
`isotty audit logs` shows what the VM wrote. Recording happens over a
separate SSH command per write; if it fails, the client is told that the note
was written but the audit record was not.

## VM Side

`isotty up` installs, on every run:

* `/usr/local/bin/isotty-notes`, a Python client with `status`, `search`,
  `read`, `create`, `append` and `changes`
* the gateway's `/epweave/mcp` as the `epweave` MCP server of each agent in
  `agent.yaml`, through `/usr/local/share/isotty/mcp-merge.py`
* `/etc/ssh/sshd_config.d/00-isotty.conf` with `ClientAliveInterval 15` and
  `ClientAliveCountMax 3`; the `00-` prefix matters because sshd keeps the
  first value it reads and the cloud image's `50-cloudimg-settings.conf`
  sets `ClientAliveInterval 120`
* the `isotty-notes` skill for each agent in `agent.yaml`, under
  `~/.claude/skills/` for Claude and `~/.codex/skills/` for Codex

Running on every `up` means enabling ep_weave on an existing VM only needs
another `isotty up`.

## Lifecycle

The gateway lives exactly as long as the attach session. When the human
detaches, or their machine sleeps, ep_weave itself is unreachable as well,
so there is nothing to buffer against.

Writes are therefore never queued. A queue would let the agent believe a
note was written when it was not, reorder appends against human edits, and
lose the queue with `isotty down`. Instead a failed write fails within
seconds with a message that says why and what to do:

```text
isotty-notes: the ep_weave gateway at http://127.0.0.1:61001/epweave is not reachable (...).
It is available only while the host is attached with `isotty attach`; writes are not queued.
Keep the note as a Markdown file under /workspace and retry after re-attach.
```

Three settings make that failure prompt instead of a hang:

* sshd `ClientAliveInterval`/`ClientAliveCountMax` in the VM, so a dead
  session's `-R` listener is released within about 45 seconds instead of
  the 6 minutes the cloud image's defaults allow
* `ServerAliveInterval`/`ServerAliveCountMax` on the attach `ssh`
* `ExitOnForwardFailure=yes` on the attach `ssh`, so a forward that cannot
  bind fails the attach instead of leaving a session whose forwards silently
  point nowhere; this applies to `-L` forwards as well

A second `isotty attach` to the same VM therefore fails while the first is
alive; use `isotty attach --no-host` for the second terminal. That flag
skips the forwards and the gateway.

## Security Posture

* ep_weave credentials never leave the host
* the VM can write only under its own title prefix and cannot delete
* the prefix is pinned on the host; the synced copy cannot widen it
* pads are data written by other authors; the VM skill tells the agent that
  its user's instructions take precedence over anything a pad asks
* the gateway binds to `127.0.0.1` on both ends and lives only while attached
* ep_weave's own pads and editor are not relayed to the VM; a page the VM
  asks the host browser to open (see [OPECO.md](OPECO.md)) has the browser's
  reach, which includes a local ep_weave
