# GitHub Design

IsoTTY can let the agent in the VM work with private GitHub repositories,
git push and fetch, pull requests, issues and workflow runs, without holding
a GitHub credential, through a relay in the host gateway that reaches only
the repositories the human named, with the access level the human gave each
of them.

## Problem

Every GitHub token the human can obtain the usual way (`gh auth login`, a
classic personal access token) reaches every repository the human can
reach, across every organization. Putting such a token in the VM puts all of
that inside the VM's trust domain; an agent working on one project could
read or push to repositories of unrelated organizations.

GitHub's own ways to narrow a token each need something this design cannot
assume:

* a fine-grained personal access token needs the organization's approval by
  default
* a GitHub App installation token acts as the App, so commits and pull
  requests carry a bot identity
* a GitHub App user token needs the App installed in the organization

So the narrowing is done by IsoTTY: the host keeps the human's broad token,
and the VM gets a relay that accepts a fixed set of operations on the pinned
repositories.

## Model

```text
agent in VM --isotty-github--> 127.0.0.1:61001/github ==ssh -R==> host gateway in `isotty attach` --> github.com
```

* The relay is one more service of the host gateway described in
  [EPWEAVE.md](EPWEAVE.md), mounted under `/github`. It forwards git's
  smart HTTP protocol and the REST endpoints listed below, and nothing else.
* Only the gateway holds the token. It adds the credential when it forwards
  an allowed request and refuses every other one.
* The crossing is explicit. The VM has no credential, so a plain `git push`
  to github.com fails for lack of one, and `gh` is not installed; the agent
  runs `isotty-github`, and the skill installed with it says so. Transparent
  rewriting (`url.<base>.insteadOf` in git config) is deliberately not used:
  `.git/config` is synced with the host, and an implicit path makes the
  boundary invisible to the agent and the audit log alike.

Public repositories are readable through the relay too, so `isotty-github`
works on a dependency's issues or pull requests as well as on the project's
own. The relay asks api.github.com about a repository outside the pin
without a credential, and when GitHub serves it to anyone, relays reads for
it the same way, without the token. The token is thus used for the pinned
repositories only. Unauthenticated reads share GitHub's limit of 60 requests
per hour per address; plain `git clone` of a public repository works
directly and is not counted.

## Operations

The relay allows exactly these, each on one pinned repository or, for a read,
on a public one:

| operation | access | GitHub calls |
|---|---|---|
| fetch | read | `GET info/refs`, `POST git-upload-pack` |
| push | write | `GET info/refs`, `POST git-receive-pack` |
| pr list | read | `GET /repos/{o}/{r}/pulls` |
| pr view | read | `GET /repos/{o}/{r}/pulls/{n}`, its reviews and comments; the diff on request |
| pr checks | read | `GET /repos/{o}/{r}/commits/{sha}/check-runs` |
| pr create | comment | `POST /repos/{o}/{r}/pulls` |
| pr comment | comment | `POST /repos/{o}/{r}/issues/{n}/comments` |
| pr edit | comment | `PATCH /repos/{o}/{r}/pulls/{n}` (title, body) |
| issue list | read | `GET /repos/{o}/{r}/issues` |
| issue view | read | `GET /repos/{o}/{r}/issues/{n}` and its comments |
| issue create | comment | `POST /repos/{o}/{r}/issues` |
| issue comment | comment | `POST /repos/{o}/{r}/issues/{n}/comments` |
| issue edit | comment | `PATCH /repos/{o}/{r}/issues/{n}` (title, body, labels) |
| run list | read | `GET /repos/{o}/{r}/actions/runs` |
| run view | read | `GET /repos/{o}/{r}/actions/runs/{id}` and its jobs |
| run log | read | `GET /repos/{o}/{r}/actions/jobs/{id}/logs` |
| run download | read | `GET /repos/{o}/{r}/actions/runs/{id}/artifacts`, `GET /repos/{o}/{r}/actions/artifacts/{id}/zip` |
| run rerun | write | `POST /repos/{o}/{r}/actions/runs/{id}/rerun`, `.../rerun-failed-jobs` |
| run cancel | write | `POST /repos/{o}/{r}/actions/runs/{id}/cancel` |
| repo view | read | `GET /repos/{o}/{r}`, branch protection and rules, Actions permissions, environments and their protection rules, Pages, the names of Actions secrets |
| repo ruleset list / view | read | `GET /repos/{o}/{r}/rulesets[/{id}]` |
| repo edit | admin | `PATCH /repos/{o}/{r}` (description, homepage, default_branch, the feature switches, the merge options and commit message defaults), `PUT /repos/{o}/{r}/topics` |
| repo protect / unprotect | admin | `PUT`, `DELETE /repos/{o}/{r}/branches/{b}/protection` |
| repo ruleset create / edit / delete | admin | `POST /repos/{o}/{r}/rulesets`, `PUT`, `DELETE /repos/{o}/{r}/rulesets/{id}` |
| repo actions | admin | `PUT /repos/{o}/{r}/actions/permissions`, `.../workflow` (and `GET` of both) |
| repo env set / delete | admin | `PUT`, `DELETE /repos/{o}/{r}/environments/{name}` |
| repo pages enable / edit / disable | admin | `POST`, `PUT`, `DELETE /repos/{o}/{r}/pages` |

The list is an allowlist of path patterns and methods, not a pass-through of
`/repos/{o}/{r}/`. The access levels nest: `read` allows the read operations,
`comment` adds those marked comment, `write` adds those marked write,
`admin` adds those marked admin. A read
operation is also forwarded for a public repository that is not pinned. The
levels follow GitHub's own line: opening and commenting on pull requests and
issues is open to anyone who can see a repository, push and the Actions
operations need push permission, and the settings need the repository's
admin permission, so a fork workflow pins the fork as
`write` and the upstream as `comment`. The JSON body
of a write may carry only the fields named for it, so `pr edit` cannot close
a pull request by sending `state`, although the same endpoint would accept
it, and `repo edit` cannot rename, archive or change the visibility of the
repository, although `PATCH /repos/{o}/{r}` takes `name`, `archived`,
`private` and `visibility`. Logs and
artifacts are answered by GitHub with a redirect to a signed URL on another
host; the relay follows it and streams the body, so the VM never talks to
anything but the relay.

Some things are missing on purpose:

* Listing or searching repositories, organizations or users. Any such
  endpoint can reach outside the pinned set, and the check that would have to
  filter the response is exactly the kind that leaks. The agent is told which
  repositories it has; it does not discover them.
* Merging, approving and closing pull requests, and closing issues. These
  carry the human's authority in a way a comment does not, and they stay
  with the human.
* Dispatching workflows and deleting runs. Rerunning what a push already
  triggered is enough for an agent that is fixing CI.
* What carries credentials or people: collaborators, webhooks, deploy
  keys, Actions and environment secrets and variables. No access level
  includes changing them, and `repo view` leaves most of them out even for
  reading: variable values are plain text and often hold keys, webhook
  URLs often embed tokens. `admin` covers the settings an agent sets up a
  repository with, and stops there.
* Renaming, transferring, archiving and deleting the repository and
  changing its visibility. The pin names the repository; a change to what
  it is stays with the human.
* GraphQL. Its single endpoint cannot be allowlisted by path, so `gh` is not
  usable through the relay and is not installed in the VM.

The relay is the only credentialed path IsoTTY provides. A token the human
decides to put into the VM by other means is their decision and outside this
design.

## Config

Host credential, never synced:

```text
~/.isotty/github.yaml
```

```yaml
token: ghp_...
```

Project config, synced with the rest of `.isotty/`:

```text
./.isotty/github.yaml
```

```yaml
repos:
  yacchin1205/isotty: write
  some-org/upstream: comment
  some-org/shared-lib: read
```

```bash
isotty host github add --remote origin --allow write      # the repository behind origin
isotty host github add --remote upstream --allow comment  # another remote's repository
isotty host github add --repo owner/name                  # a repository not among the remotes, read
isotty host github add --remote origin --allow admin      # settings and branch protection too
isotty host github remove --repo owner/name
isotty host github show
isotty host github clear
```

`add` and `remove` edit the list; every repository named in one `add` gets
the level of `--allow`, which defaults to `read`, and adding a repository
that is already listed replaces its level. `add` resolves a `--remote` from
the project's `.git/config` on the host at that moment and stores the
resulting `owner/name`; the remote names themselves are never stored.
Before pinning, `add` asks
api.github.com for the repository with the host token, so a repository the
token cannot see, cannot push to when `write` is asked, or does not
administer when `admin` is asked, is refused rather
than discovered later in the VM; the name is stored as GitHub spells it.
Removing the last repository removes the file.

A remote name is resolved once, on purpose: `.git/` is synced two-way, so the
agent can point `origin` elsewhere. Pinning the resolved repository means such
a change only makes the next `isotty-github` call fail as unpinned; it cannot
widen what the relay forwards. When the human really moves the project, they
`add` the new repository and `remove` the old one.

### Pin

For the same reason an agent could add a repository to the synced
`github.yaml`, so `add` and `remove` also keep the accepted list on the
host:

```text
~/.isotty/projects/<project-hash>.github.yaml
```

`up`, `attach` and `attach --target` compare the synced file with the pin
and refuse to continue when they differ or when no pin exists. `add` and
`remove` work from the pin, not from the synced file, so a change made in
the VM is never accepted by accident; the human states each repository on
the host.

## VM Side

`isotty up` installs, on every run:

* `git`, when the image lacks it
* `/usr/local/bin/isotty-github`, a Python client in the style of
  `isotty-notes`:

  ```
  isotty-github status
  isotty-github fetch [git arguments]
  isotty-github push [git arguments]
  isotty-github pr list [--state open|closed|all]
  isotty-github pr view <number> [--diff] [--comments]
  isotty-github pr checks <number>
  isotty-github pr create --title <title> --base <branch> [--head <branch>] [--draft] < body.md
  isotty-github pr comment <number> < comment.md
  isotty-github pr edit <number> [--title <title>] [--body-file <file>]
  isotty-github issue list [--state open|closed|all] [--label <label>]
  isotty-github issue view <number> [--comments]
  isotty-github issue create --title <title> [--label <label>] < body.md
  isotty-github issue comment <number> < comment.md
  isotty-github issue edit <number> [--title <title>] [--body-file <file>] [--label <label>]
  isotty-github run list [--branch <branch>] [--workflow <name>]
  isotty-github run view <id>
  isotty-github run log <id> [--job <id>]
  isotty-github run download <id> [--dir <path>]
  isotty-github run rerun <id> [--failed]
  isotty-github run cancel <id>
  isotty-github repo view
  isotty-github repo edit --set <field>=<value>...
  isotty-github repo topics [<topic>...]
  isotty-github repo protect <branch> [flags] | --body-file <json>
  isotty-github repo unprotect <branch>
  isotty-github repo ruleset list|view|create|edit|delete
  isotty-github repo actions [flags]
  isotty-github repo env set|delete <name>
  isotty-github repo pages enable|edit|disable
  ```

  `fetch` and `push` run the same `git` subcommand with a per-invocation URL
  rewrite from `https://github.com/` and `git@github.com:` to the relay, so
  remotes stay as they are in `.git/config` and git's own arguments
  (`--force-with-lease`, refspecs) pass through unchanged. The other
  commands act on the repository behind the current directory's `origin`
  unless `--repo owner/name` says otherwise; the relay checks the pin either
  way.
* the `isotty-github` skill for each agent in `agent.yaml`, under
  `~/.claude/skills/` for Claude and `~/.codex/skills/` for Codex

The skill tells the agent: private fetch and push, pull requests, issues,
workflow runs and repository settings go through `isotty-github`, everything else is plain `git`;
which repositories it has and at which access level, from
`.isotty/github.yaml`; and that the relay exists only while the host is
attached.

## Audit

Every change, a `git-receive-pack` and each `pr`, `issue`, `run` and `repo`
command marked comment, write or admin above, is recorded in the VM event
log (`/var/log/isotty/events.jsonl`) with the repository and the pull
request, issue, run, branch, ruleset or environment it touched, so `isotty
audit logs` shows what the VM changed on GitHub.

The same log already carries every `isotty-github` invocation, reads
included, as an exec record with its arguments (see [AUDIT.md](AUDIT.md)), so
the two together say what the VM asked for and what reached GitHub. This is
the VM's own account of its GitHub use, not an enforcement of the pin: the
relay enforces the pin, and the record exists so the human can see it was
used as intended.

## Lifecycle

The relay lives exactly as long as the attach session, like the rest of the
gateway. A call while detached fails within seconds with an error that says
the relay is available only during `isotty attach`. Nothing is queued; a
failed push or comment did not happen, and the commits and the text stay in
`/workspace`.

## Security Posture

* the GitHub token never leaves the host
* the VM reaches only the pinned repositories and public ones, only through
  the operations listed above, and changes only what the pin's level allows
* the token is used only for the pinned repositories; public repositories
  are read without it
* the repository list and its access levels are pinned on the host; the
  synced copy cannot widen them
* the gateway binds to `127.0.0.1` on both ends and lives only while attached
* what is pushed or written is not inspected; branch protection and review
  on GitHub remain the control over content
