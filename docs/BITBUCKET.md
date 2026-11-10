# Bitbucket Design

IsoTTY can let the agent in the VM work with Bitbucket Cloud repositories
the way [GITHUB.md](GITHUB.md) describes for GitHub: git push and fetch,
pull requests, pipelines and repository settings, through a relay in the
host gateway that reaches only the repositories the human named, with the
access level the human gave each of them. This document covers what
differs; the problem, the trust model, the pin and the audit are the same
and are not repeated. Bitbucket Data Center is another product with
another API and is not covered.

## Model

```text
agent in VM --isotty-bitbucket--> 127.0.0.1:61001/bitbucket ==ssh -R==> host gateway in `isotty attach` --> bitbucket.org
```

* The relay is mounted under `/bitbucket`. It shares its mechanics with
  the GitHub relay (package `forge`): the pin decision, the git smart HTTP
  forwarding, the REST allowlist with per-operation fields, and the audit
  record made before the VM hears the result.
* A repository is named `workspace/repo_slug`, as Bitbucket's `full_name`
  spells it. Remote URLs of all three shapes resolve to it, the HTTPS one
  with the user's name in it (`https://user@bitbucket.org/...`) as
  Bitbucket's clone dialog writes it, and SSH through
  `altssh.bitbucket.org:443` as well.
* Public repositories are readable, as on GitHub: the relay reads the
  repository's `is_private` with the token and relays reads when it is
  false.

## Credential

The token is an Atlassian account **API token with scopes**, created under
the account's security settings for the Bitbucket product. It acts as the
human across every workspace the account is in, like the `gh` CLI's token
on GitHub, and Bitbucket's own narrower credentials each need something
this design cannot assume: a repository, project or workspace access token
acts as a bot of its own (`{id}@bots.bitbucket.org`), so commits and pull
requests carry no human name, and OAuth needs a consumer registered in a
workspace. So, as on GitHub, the narrowing is IsoTTY's. App passwords,
the former way, stopped working in June 2026.

The token has an expiry date, which Bitbucket requires; when it passes,
`attach` keeps working but every relayed call answers 401, and the human
creates a new one. The scopes a pin needs:

| pin | scopes |
|---|---|
| read | `read:repository:bitbucket`, `read:pullrequest:bitbucket`, `read:pipeline:bitbucket` |
| comment | + `write:pullrequest:bitbucket` |
| write | + `write:repository:bitbucket`, `write:pipeline:bitbucket` |
| admin | + `admin:repository:bitbucket` |

plus `read:user:bitbucket` and `read:workspace:bitbucket`, which `add`
needs to read the token's own permission on a repository.

The relay sends the token as a bearer token to the REST API, and to git
over HTTPS as the password of the static user `x-bitbucket-api-token-auth`,
so nothing but the token is configured.

## Access

The levels follow Bitbucket's repository permissions: `read` is what any
member sees, `comment` adds pull requests and their comments, which need
no more than read on the repository, `write` is the write permission
(push, stopping a pipeline), `admin` is the admin permission (settings and
branch restrictions). `add` reads the token's permission on the
repository from the workspace's permission list and refuses `write` below
write and `admin` below admin.

| operation | access | API calls under `/repositories/{workspace}/{repo_slug}` |
|---|---|---|
| fetch | read | `GET info/refs`, `POST git-upload-pack` |
| push | write | `GET info/refs`, `POST git-receive-pack` |
| pr list / view / checks | read | `GET /pullrequests[/{id}]`, `/comments`, `/activity`, `/commits`, `/diff`, `/diffstat`, `/statuses`; `GET /commit/{sha}/statuses` |
| pr create | comment | `POST /pullrequests` (title, description, draft, close_source_branch, source.branch.name, destination.branch.name) |
| pr comment | comment | `POST /pullrequests/{id}/comments` (content.raw) |
| pr edit | comment | `PUT /pullrequests/{id}` (title, description) |
| pipeline list / view / log | read | `GET /pipelines[/{uuid}]`, `/steps[/{uuid}]`, `/steps/{uuid}/log` |
| pipeline stop | write | `POST /pipelines/{uuid}/stopPipeline` |
| repo view | read | `GET /repositories/{w}/{r}`, `/branch-restrictions`, `/branching-model` |
| repo edit | admin | `PUT /repositories/{w}/{r}` (description, language, fork_policy, enforced_signed_commits) |
| repo restriction create / edit / delete | admin | `POST /branch-restrictions`, `PUT`, `DELETE /branch-restrictions/{id}` |

What differs from GitHub, beyond the names:

* **No issues.** Bitbucket Cloud removed its issue tracker and wikis in
  August 2026, API included; issues live in Jira, which is another API
  and outside this relay.
* **No rerun.** Bitbucket's API cannot rerun a pipeline or its failed
  steps; the only way to run one is to start a new pipeline for a branch
  or commit, which is the workflow dispatch GITHUB.md leaves out, and is
  left out here too. A push runs the pipeline again; `pipeline stop` is
  the one control.
* **A pull request names its branches inside objects.** `source` and
  `destination` carry a `branch`, and may carry a `repository`, which
  makes a pull request from a fork into another repository. The relay's
  field check follows dotted paths (`source.branch.name`), so a
  `repository` anywhere in them is refused, as GitLab's
  `target_project_id` is; `reviewers`, who are people, is refused with
  it. Merging, approving, declining and requesting changes are endpoints
  of their own and are not in the allowlist, so no field needs guarding
  against them.
* **Pipelines are named by UUIDs** in braces, sent percent-encoded; the
  client takes the build number the Bitbucket page shows and finds the
  UUID among the recent pipelines.
* **A pull request's diff is a redirect** to the repository diff on the
  same host, which needs the token as the first call did; the relay keeps
  the credential on a redirect to the same host and drops it on one to
  another host, where a step's log is served from a signed URL.
* **Pages are named in the body**, as an absolute URL of
  api.bitbucket.org in `next`, not in a `Link` header; the client takes
  the path from it and asks the relay again.

`repo edit` accepts the fields of the repository details page and not
`name`, which moves the repository to another slug, `is_private` or
`project`. Users and groups, webhooks, deploy keys, pipeline variables and
deployment environments are not reachable at any level, as on GitHub.

## Config

Host credential, never synced:

```text
~/.isotty/bitbucket.yaml
```

```yaml
token: ATATT3x...
```

`isotty host bitbucket login` writes it: it prints where to create the
token and which scopes it needs, asks for the token on the terminal
without echo, confirms it against `/2.0/user`, and replaces what was
there, which is what to do when the token expires. The token is read when
`attach` starts, so a new value takes effect at the next attach.

Project config, synced with the rest of `.isotty/`:

```text
./.isotty/bitbucket.yaml
```

```yaml
repos:
  my-team/app: write
  my-team/upstream: comment
```

```bash
isotty host bitbucket login
isotty host bitbucket add --remote origin --allow write
isotty host bitbucket add --repo my-team/upstream --allow comment
isotty host bitbucket add --remote origin --allow admin      # settings and branch restrictions too
isotty host bitbucket remove --repo my-team/upstream
isotty host bitbucket show
isotty host bitbucket clear
```

`add`, `remove`, `clear` and `show` behave as for GitHub. `add` asks
api.bitbucket.org for each repository with the token before pinning, so a
repository the token cannot see, a `write` pin without the write
permission or an `admin` pin without admin is refused on the host; the
name is stored as Bitbucket spells it. The pin is
`~/.isotty/projects/<project-hash>.bitbucket.yaml`.

## VM Side

`isotty up` installs `/usr/local/bin/isotty-bitbucket` and the
`isotty-bitbucket` skill for each configured agent:

```
isotty-bitbucket status
isotty-bitbucket fetch [git arguments]
isotty-bitbucket push [git arguments]
isotty-bitbucket pr list|view|checks|create|comment|edit
isotty-bitbucket pipeline list|view|log|stop
isotty-bitbucket repo view|edit
isotty-bitbucket repo restriction list|view|create|edit|delete
```

`fetch` and `push` run git with the project's Bitbucket remotes rewritten
to the relay for that one invocation, each by its exact URL, since
Bitbucket's HTTPS remotes carry the user's name; the bare forms of the
three URL shapes are rewritten too, for a URL given on the command line.
The other commands act on the repository behind `origin` unless `--repo
workspace/repo_slug` is given.

## Audit

Pushes and writes are recorded as `bitbucket-<operation>` events with the
repository and the pull request or comment id, the pipeline UUID or the
branch restriction id, in the VM event log, as GitHub's are.
