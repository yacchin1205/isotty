# Google Design

IsoTTY can let the agent in the VM use Google APIs, Docs, Sheets, Forms,
Google Cloud and the like, as a service account the human chose, without a
credential of the service account ever entering the VM. The host keeps its
key, or impersonates it with the human's gcloud login; the attach gateway
hands the VM short-lived access tokens for the scopes the human pinned.

## Problem

The usual way to give an agent Google access is the human's own OAuth token,
which reaches everything the human can open in Drive. That is far more than
any one project needs, and nothing narrows it per project.

A service account is the opposite: it reaches only the files that were
shared with its address and the roles granted to it, in the APIs enabled
for its project, within the scopes a token was issued for. Google enforces
all of them. Sharing a file with the service account, or granting it a role
on a project or a billing account, is an ordinary operation the human
already knows. So the unit of permission is the service account, and
IsoTTY's job is only to issue tokens for it without letting its credential
out of the host.

## Model

```text
agent in VM --isotty-google exec-- tool --> googleapis.com
                 |
                 +--GET /google/token--> 127.0.0.1:61001 ==ssh -R==> host gateway in `isotty attach`
                                                                       |
                                                 key in ~/.isotty/google/, or the human's gcloud login
```

* The host gateway described in [EPWEAVE.md](EPWEAVE.md) mounts `/google`,
  whose one operation issues an access token for the pinned scopes, in one
  of two ways the human chose at `enable`:
  * with a key: the gateway signs a JWT with the service account key and
    exchanges it at Google's token endpoint;
  * by impersonation: the gateway asks the IAM Credentials API
    (`generateAccessToken`) with the token of the host's active gcloud
    account, as the DNS relay calls Cloud DNS. No key exists; the human's
    login must hold `roles/iam.serviceAccountTokenCreator` on the service
    account.

  Both exist because an organization created since 2024 forbids service
  account keys by default, while a project outside an organization, or one
  whose human cannot be granted impersonation, has only the key. The token
  lives one hour; the gateway reuses it until it is about to expire.
* The VM holds no credential of the service account. `isotty-google exec` fetches a token and runs a
  command with it in `GOOGLE_OAUTH_ACCESS_TOKEN`; the command talks to
  googleapis.com directly. API calls are not relayed: what the token can
  reach is already bounded by Google, and the VM has network access anyway.
* The VM is created without an attached service account and without
  scopes, so Application Default Credentials in the VM find nothing. Without
  that, every Google client library in the VM would silently use the GCE
  project's default service account through the metadata server.

## Config

Host keys, never synced, one file per service account named by its address;
`enable --key` puts them there:

```text
~/.isotty/google/<service-account-email>.json
```

Project config, synced with the rest of `.isotty/`:

```text
./.isotty/google.yaml
```

```yaml
service_account: docs-reader@my-project.iam.gserviceaccount.com
credential: key
scopes:
  - documents.readonly
  - spreadsheets.readonly
```

```bash
isotty host google enable --key ~/Downloads/my-project-1234.json \
    --scope documents.readonly --scope spreadsheets.readonly
isotty host google enable --impersonate isotty@my-project.iam.gserviceaccount.com \
    --scope cloud-platform
isotty host google show
isotty host google disable
```

`credential` is `key` or `impersonation`, and is part of the agreement, not
inferred from whether a key file exists: a key left under
`~/.isotty/google/` by another project must not change how this one gets
its tokens. A `google.yaml` without it was written before impersonation and
means `key`.

`enable` issues one token and pins nothing when that fails, so a service
account that cannot be used is found at `enable` and not at the next
`attach`. The failure, and `enable --help`, print how to make the service
account ready: for a key, how to create one; for impersonation, the grant
of `roles/iam.serviceAccountTokenCreator` on the service account to the
human's account. The guide is not printed on every `enable`, which the
human runs once the service account is ready. What the service account may
reach is the roles and the sharing the human gives it, and the guide says
only that.

`--key` names the key file Google Cloud Console produced; `enable` reads the
service account address from it, copies it under `~/.isotty/google/` with
mode 600, and refuses a file that lies inside the project, because the
project is synced into the VM. `--impersonate` names the service account
by its address and stores nothing on the host. Once a project has a pinned
service account, `enable` may be run with `--scope` alone to change the scopes or to accept a reviewed
change; the account stays the pinned one. Scopes are written without the
`https://www.googleapis.com/auth/` prefix and pinned exactly as given, and
the whole set is written on every `enable`.

One project has one service account: a project that needs two different
reaches is two service accounts' worth of sharing, and the human picks
which one the agent is. `disable` leaves the imported key in place for other
projects.

Scopes matter even though sharing already bounds the files: the same
service account may be shared a form with edit rights for one project and
pinned `forms.body.readonly` for another, and the token then cannot write
it.

### Pin

`.isotty/` is synced two-way, so an agent could change the service account
or the credential, or widen the scopes, in `google.yaml`. `isotty host google enable` records
the accepted values on the host:

```text
~/.isotty/projects/<project-hash>.google.yaml
```

`up`, `attach` and `attach --target` compare the synced file with the pin
and refuse to continue when they differ or when no pin exists. Accepting a
change means running `isotty host google enable` on the host again.

## VM Side

`isotty up` installs, on every run:

* `/usr/local/bin/isotty-google`, a Python client in the style of
  `isotty-notes`:

  ```
  isotty-google status
  isotty-google exec -- <command> [arguments]
  isotty-google token
  ```

  `exec` runs the command with `GOOGLE_OAUTH_ACCESS_TOKEN` set, so the
  token appears in no command line and no shell history. `token` prints it
  for the rare tool that cannot take it from the environment.
* the `isotty-google` skill for each agent in `agent.yaml`, under
  `~/.claude/skills/` for Claude and `~/.codex/skills/` for Codex

Tools must accept the token from that variable. For the `googleapis` Node
library that is an `OAuth2` client with `setCredentials({access_token})` in
place of `GoogleAuth`; the shinshu-workflow CLIs get that change.

## Audit

Every token the gateway mints is recorded in the VM event log
(`/var/log/isotty/events.jsonl`) with the service account and the scopes;
the same log carries every `isotty-google exec` invocation with the command
it ran, document ids included, as an exec record. Together they say which
identity the VM used and what it asked for. Google's side holds the
authoritative record of what the service account did.

## Lifecycle

Tokens are issued only while the host is attached. A token already in hand
keeps working until it expires, at most one hour after the human detached;
after that every call fails until the next attach. Nothing is queued.

## Security Posture

* the service account key, or the human's gcloud login that impersonates
  it, never leaves the host
* the VM reaches only what was shared with the service account, through the
  pinned scopes; Google enforces both
* service account and scopes are pinned on the host; the synced copy cannot
  widen them
* the VM has no attached GCE service account, so no other Google credential
  is reachable from it
* the gateway binds to `127.0.0.1` on both ends and lives only while attached
* a token that leaks out of the VM is useful for at most an hour and only
  within the same bounds
