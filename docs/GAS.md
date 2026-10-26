# Apps Script Design

IsoTTY can let the agent in the VM run functions of Google Apps Script
projects as the human, through the Apps Script API, without the human's
Google credential ever entering the VM. The host keeps the login; the
attach gateway runs functions of the scripts the human pinned.

## Problem

Work that lives in Apps Script, the processing behind a spreadsheet or a
form, mail it sends, PDFs it writes, cannot be exercised from outside the
script. An agent that edits the script or the sheet can check its work only
by running the script, and the only way to run a function from outside is
`scripts.run`.

`scripts.run` does not take a service account, so the [Google
design](GOOGLE.md) does not apply: the call must carry a token of a human,
issued to an OAuth client of the script's own Cloud project, covering every
scope the script uses. That token is the human's authorization for the
whole script, not for a function. What IsoTTY can do is keep it on the
host, let the VM ask for one function at a time in a pinned script, and
record each ask.

## Model

```text
agent in VM --isotty-gas run script f [args]--> 127.0.0.1:61001/gas ==ssh -R==> host gateway in `isotty attach`
                                                                                    |
                                                                        login in ~/.isotty/gas/  --scripts.run--> script.googleapis.com
```

* The host gateway described in [EPWEAVE.md](EPWEAVE.md) mounts `/gas`,
  whose one operation runs a function: the gateway checks the script
  against the pin, refreshes the human's access token, posts the call to
  `scripts.run` and relays the answer. The VM never sees the token.
* Functions run at the script's latest saved code (`devMode`), the code
  the human is editing, with no deployment step in between. Google grants
  that to the script's owner only, so the account that logs in must own
  every script it pins.
* The script runs as the human, with everything the human authorized the
  script to do. The pin names the scripts; what a function touches is the
  script's business, and the scopes in the pin say how far that can go.
  Which function to call is the agent's choice, and the record keeps it.
* One run lasts at most six minutes, Apps Script's limit; the gateway waits
  seven. A run that outlives it has no answer, although it may have done its
  work. The record says so.

## Config

Host logins, never synced, one file per OAuth client:

```text
~/.isotty/gas/<client-id>.yaml
```

Each holds the client, the refresh token Google issued to it, the account
that authorized and the scopes it granted.

Project config, synced with the rest of `.isotty/`, one entry per script:

```text
./.isotty/gas.yaml
```

```yaml
scripts:
  - id: 1AbCdEf_GhIjKlMnOpQrStUvWxYz
    client: 123456-abc.apps.googleusercontent.com
    account: human@example.com
    scopes:
      - https://www.googleapis.com/auth/spreadsheets
      - https://www.googleapis.com/auth/script.send_mail
```

The client and the scopes belong to the script, not to the project: each
script's Cloud project issues its own clients, and each script's manifest
has its own scopes. Two scripts in the same Cloud project share a client
file and a login; two scripts in different Cloud projects need one each.

```bash
isotty host gas add --script 1AbCdEf_GhIjKlMnOpQrStUvWxYz \
    --client ~/Downloads/client_secret.json \
    --scope https://www.googleapis.com/auth/spreadsheets \
    --scope https://www.googleapis.com/auth/script.send_mail
isotty host gas show
isotty host gas remove --script 1AbCdEf_GhIjKlMnOpQrStUvWxYz
isotty host gas clear
```

`add` pins each `--script` given as its own entry with the client and
scopes given, replacing the entry of a script already pinned; `remove`
drops one; `clear` drops them all. A script's scope set grows the same way:
`add` the script again with the longer list.

What `add` needs from the Google side (`isotty host gas add -h` walks
through it with the Console pages):

* The script is attached to a standard Cloud project, not the default one,
  with the Apps Script API enabled; `--client` names the JSON of a desktop
  application OAuth client of that project. The API accepts no token from a
  client of another project.
* `--script` is the script id, and the account that logs in owns the
  script.
* The script has been deployed as "API executable" at least once, from
  Deploy > New deployment. Google's reference addresses `scripts.run` to
  such a deployment; what was observed is that a script never deployed
  that way answers every call with "a server error occurred while reading
  from storage. Error code NOT_FOUND", and keeps working once a deployment
  was made, even after it is deleted again.

`add` verifies each script before pinning it: it asks the API to run a
function the script cannot have. The API loads the script with the token
first and looks the function up second, so "function not found" proves the
path end to end, while the storage error above, a 403 or any other answer
names what is missing and the pin is refused. Nothing runs; the attempt
shows in the script's execution log.
* `--scope` lists the scopes the script uses, the full URLs from its
  overview page. The token must cover them all or calls fail; the login asks
  for exactly them, plus `openid` and `email` so the account is known.

`add` opens the browser for the authorization the first time a client is
used and whenever the scopes of the scripts using that client grow beyond
what was granted. The account that logs in is the one the VM runs the
script as, and it is written into the entry.

### Pin

`.isotty/` is synced two-way, so an agent could add a script or widen the
scopes in `gas.yaml`. `add` and `remove` record the accepted list on the
host and build on that copy, not on the synced file:

```text
~/.isotty/projects/<project-hash>.gas.yaml
```

`up`, `attach` and `attach --target` compare the synced file with the pin
and refuse to continue when they differ or when no pin exists. Accepting a
change means running `isotty host gas add` or `remove` on the host.

## VM Side

`isotty up` installs, on every run:

* `/usr/local/bin/isotty-gas`, a Python client in the style of
  `isotty-notes`:

  ```
  isotty-gas status
  isotty-gas run <id> <function> ['<json array of arguments>']
  ```

  `run` prints the function's return value as JSON. A script error prints
  the script's message and stack trace and exits non-zero; a script outside
  the pin is refused by the gateway with the pinned list. `status` shows
  each pinned script with the account it runs as.
* the `isotty-gas` skill for each agent in `agent.yaml`, under
  `~/.claude/skills/` for Claude and `~/.codex/skills/` for Codex

## What the pin says

The pin is an agreement between the human and the agent on what the agent
may ask for, and it is exact about that: these scripts, each as the account
that authorized it. It is not a statement of what the scripts do. A function runs with the human's
full authorization for its script, so it may read or write any file the
script can open, send mail, or call other services, whatever the script's
code does. The scopes in the pin are the outer bound Google enforces; the
scripts are the bound IsoTTY enforces; the code in between is the human's
to review.

The function and its parameters are part of the ask, not of the agreement.
They are passed through and recorded. The pin does not list functions
because that would narrow only the entry, not what runs behind it: a
receptionist function that takes a name does whatever the name selects. A
project that wants a fixed target writes it into the script, not into the
pin.

## Audit

Every run is recorded in the VM event log (`/var/log/isotty/events.jsonl`)
with the account, the script, the function, its parameters and how it
ended: `done`, the script's own error, the API's refusal, or `no response`
when the host got no answer. The last one is the case the human has to look
at: the function may have run, in part or in full.

The same log carries every `isotty-gas` invocation as an exec record, so
the two together say what the VM asked for and what the host did with it.
Apps Script's own execution log holds the authoritative record of what the
script did.

## Lifecycle

Runs go through the attached host only. When the human detaches, `run`
fails within seconds; a run already under way on Google's side continues,
and its answer is lost with the connection. Nothing is queued.

A refresh token lives until the human revokes it in their Google account
or Google expires it; `add` logs in again when it is gone.

## Security Posture

* the human's refresh tokens and the OAuth client secrets never leave the host
* the VM can run functions of the pinned scripts only; the gateway refuses
  other scripts before any token is used
* the functions run as the human, within the scopes the human granted;
  Google enforces the scopes, the script's code decides the rest
* each script's client, account and scopes are pinned on the host; the
  synced copy cannot widen them
* the gateway binds to `127.0.0.1` on both ends and lives only while attached
* every run, parameters included, is recorded before the VM hears its outcome
