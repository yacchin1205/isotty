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

Public repositories need no credential and are not the relay's concern; the
VM clones them with plain `git` directly.

## Operations

The relay allows exactly these, each on one pinned repository:

| operation | access | GitHub calls |
|---|---|---|
| fetch | read | `GET info/refs`, `POST git-upload-pack` |
| push | write | `GET info/refs`, `POST git-receive-pack` |
| pr list | read | `GET /repos/{o}/{r}/pulls` |
| pr view | read | `GET /repos/{o}/{r}/pulls/{n}`, its reviews and comments; the diff on request |
| pr checks | read | `GET /repos/{o}/{r}/commits/{sha}/check-runs` |
| pr create | write | `POST /repos/{o}/{r}/pulls` |
| pr comment | write | `POST /repos/{o}/{r}/issues/{n}/comments` |
| pr edit | write | `PATCH /repos/{o}/{r}/pulls/{n}` (title, body) |
| issue list | read | `GET /repos/{o}/{r}/issues` |
| issue view | read | `GET /repos/{o}/{r}/issues/{n}` and its comments |
| issue create | write | `POST /repos/{o}/{r}/issues` |
| issue comment | write | `POST /repos/{o}/{r}/issues/{n}/comments` |
| issue edit | write | `PATCH /repos/{o}/{r}/issues/{n}` (title, body, labels) |
| run list | read | `GET /repos/{o}/{r}/actions/runs` |
| run view | read | `GET /repos/{o}/{r}/actions/runs/{id}` and its jobs |
| run log | read | `GET /repos/{o}/{r}/actions/jobs/{id}/logs` |
| run download | read | `GET /repos/{o}/{r}/actions/runs/{id}/artifacts`, `GET /repos/{o}/{r}/actions/artifacts/{id}/zip` |
| run rerun | write | `POST /repos/{o}/{r}/actions/runs/{id}/rerun`, `.../rerun-failed-jobs` |
| run cancel | write | `POST /repos/{o}/{r}/actions/runs/{id}/cancel` |
| repo view | read | `GET /repos/{o}/{r}`, branch protection and rules, Actions permissions, environments and their protection rules, Pages, the names of Actions secrets |

The list is an allowlist of path patterns and methods, not a pass-through of
`/repos/{o}/{r}/`. A write operation is forwarded only when the repository is
pinned as `write`; a read operation when it is pinned at all. The JSON body
of a write may carry only the fields named for it, so `pr edit` cannot close
a pull request by sending `state`, although the same endpoint would accept
it. Logs and
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
* Changing repository settings: collaborators, hooks, deploy keys, branch
  protection, secrets, variables. No access level includes them. `repo view`
  reads the settings an agent needs to work within, and leaves out even for
  reading what carries credentials or people: Actions and environment
  variables (their values are plain text and often hold keys), webhooks
  (URLs often embed tokens), collaborators and deploy keys.
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
  some-org/shared-lib: read
```

```bash
isotty host github enable                              # the repository behind origin, write
isotty host github enable --remote upstream:read       # plus another remote's repository
isotty host github enable --repo owner/name:read       # plus a repository not among the remotes
isotty host github show
isotty host github disable
```

`enable` resolves `origin`, and any `--remote`, from the project's
`.git/config` on the host at that moment and writes the resulting
`owner/name` list; the remote names themselves are never stored. The access
suffix defaults to `write`. The list is written whole on every `enable`, so
`show` always prints the complete set the VM can reach. `enable` fails when
origin is missing or not on github.com.

A remote name is resolved once, on purpose: `.git/` is synced two-way, so the
agent can point `origin` elsewhere. Pinning the resolved repository means such
a change only makes the next `isotty-github` call fail as unpinned; it cannot
widen what the relay forwards. When the human really moves the project, they
run `enable` again.

### Pin

For the same reason an agent could add a repository to the synced
`github.yaml`, so `isotty host github enable` also records the accepted list
on the host:

```text
~/.isotty/projects/<project-hash>.github.yaml
```

`up`, `attach` and `attach --target` compare the synced file with the pin
and refuse to continue when they differ or when no pin exists. Accepting a
change means running `isotty host github enable` on the host again.

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

Every write, a `git-receive-pack` and each `pr`, `issue` and `run` command
marked write above, is recorded in the VM event log
(`/var/log/isotty/events.jsonl`) with the repository and the pull request,
issue or run it touched, so `isotty audit logs` shows what the VM changed on
GitHub.

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
* the VM reaches only the pinned repositories, only through the operations
  listed above, and writes only where the pin says `write`
* the repository list and its access levels are pinned on the host; the
  synced copy cannot widen them
* the gateway binds to `127.0.0.1` on both ends and lives only while attached
* what is pushed or written is not inspected; branch protection and review
  on GitHub remain the control over content
