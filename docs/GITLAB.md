# GitLab Design

IsoTTY can let the agent in the VM work with GitLab projects, on gitlab.com
or on a self-hosted instance, the way [GITHUB.md](GITHUB.md) describes for
GitHub: git push and fetch, merge requests, issues and pipelines, through a
relay in the host gateway that reaches only the projects the human named,
with the access level the human gave each of them. This document covers
what differs; the problem, the trust model, the pin and the audit are the
same and are not repeated.

## Model

```text
agent in VM --isotty-gitlab--> 127.0.0.1:61001/gitlab ==ssh -R==> host gateway in `isotty attach` --> the instance
```

* The relay is mounted under `/gitlab`. It shares its mechanics with the
  GitHub relay (package `forge`): the pin decision, the git smart HTTP
  forwarding, the REST allowlist with per-operation fields, and the audit
  record made before the VM hears the result.
* A project is named `host/group/project`: the instance's host name and the
  project path, which may nest groups. The host names the instance, whose
  HTTPS URL and token the host config holds; an SSH remote on another port
  resolves to the same key.
* Public projects of a pinned instance are readable without the token, as
  on GitHub. An `internal` project is not public: it answers 404 to an
  anonymous request, and the relay treats it as such.

## Access

The levels follow GitLab's roles: `read` is what any member sees,
`comment` is what a Reporter does (issues, notes, merge requests of the
project's own branches), `write` is what a Developer does (push, pipeline
and job retry and cancel), `admin` is the part of a Maintainer's work that
sets a project up (its settings and protected branches). `add` reads the
token's role on the project and refuses `write` below Developer and `admin`
below Maintainer.

| operation | access | API calls under `/projects/{id}` |
|---|---|---|
| fetch | read | `GET info/refs`, `POST git-upload-pack` |
| push | write | `GET info/refs`, `POST git-receive-pack` |
| mr list / view / pipelines | read | `GET /merge_requests[/{iid}]`, `/notes`, `/changes`, `/pipelines` |
| mr create | comment | `POST /merge_requests` (source_branch, target_branch, title, description) |
| mr comment | comment | `POST /merge_requests/{iid}/notes` (body) |
| mr edit | comment | `PUT /merge_requests/{iid}` (title, description) |
| issue list / view | read | `GET /issues[/{iid}]`, `/notes` |
| issue create | comment | `POST /issues` (title, description, labels) |
| issue comment | comment | `POST /issues/{iid}/notes` (body) |
| issue edit | comment | `PUT /issues/{iid}` (title, description, labels) |
| pipeline list / view / log | read | `GET /pipelines[/{id}]`, `/jobs`, `GET /jobs/{id}/trace` |
| pipeline retry / cancel | write | `POST /pipelines/{id}/retry`, `/cancel` |
| job log / retry / cancel | read / write | `GET /jobs/{id}/trace`, `POST /jobs/{id}/retry`, `/cancel` |
| project view | read | `GET /projects/{id}`, `/protected_branches` |
| project edit | admin | `PUT /projects/{id}` (the general and CI/CD settings) |
| project protect / unprotect | admin | `POST /protected_branches`, `DELETE /protected_branches/{name}` |

Missing on purpose, beyond what GITHUB.md lists: merging, approving and
closing (`state_event` is not among the accepted fields), creating
pipelines, and merge requests from a fork into another project. The latter
would carry `target_project_id`, a number the relay cannot check against
the pin without asking the instance; it stays out until that is worked out.
`project edit` accepts the fields of the general and CI/CD settings pages
and not `name`, `path`, `namespace_id` or `visibility`, which change what
the pin names, nor `request_access_enabled`, the mirror and import settings
or the container expiration policy. Members, webhooks, deploy keys and
CI/CD variables are not reachable at any level, as on GitHub.

## Config

Host credentials, never synced, one file per isotty project:

```text
~/.isotty/projects/<project-hash>.gitlab-tokens.yaml
```

```yaml
instances:
  - url: https://gitlab.example.org
    token: glpat-...
```

`isotty host gitlab login --url https://gitlab.example.org`, run in the
isotty project, writes an entry: it prints where to create the token and
which permissions it needs, asks for the token on the terminal without
echo, confirms it against `/api/v4/user`, and replaces any entry for the
same host. The token is meant to be a fine-grained personal access token
(GitLab 18.10 and later) scoped to the GitLab projects this isotty project
pins, with Code Download/Push, Project Read, Merge Request and Work Item
Read/Create/Update, Pipeline Read/Update, Job Read/Run/Update, Protected
Branch Read, and User Read for the login check; an `admin` pin also needs
Project Update and Branch Protect. The token is then a second
narrowing beside the pin, and GitLab's; the relay's allowlist stays as the
part the human can read on the host. A legacy token with the `api` scope
works too, for every project the account can reach. One entry per
instance; the relay uses, for each project, the token of the instance its
host names. The token lives with the project because a fine-grained one
covers that project's pins only.

Project config, synced with the rest of `.isotty/`:

```text
./.isotty/gitlab.yaml
```

```yaml
repos:
  gitlab.example.org/group/app: write
  gitlab.example.org/group/upstream: comment
```

```bash
isotty host gitlab login --url https://gitlab.example.org
isotty host gitlab add --remote origin --allow write
isotty host gitlab add --repo gitlab.example.org/group/upstream --allow comment
isotty host gitlab add --remote origin --allow admin      # settings and protected branches too
isotty host gitlab remove --repo gitlab.example.org/group/upstream
isotty host gitlab show
isotty host gitlab clear
```

`add`, `remove`, `clear` and `show` behave as for GitHub. `add` asks the
instance for each project with its token before pinning, so a project the
token cannot see, an instance missing from the host config, a `write`
pin without the Developer role or an `admin` pin without the Maintainer
role is refused on the host; the path is stored
as GitLab spells it. The pin is `~/.isotty/projects/<project-hash>.gitlab.yaml`.

## VM Side

`isotty up` installs `/usr/local/bin/isotty-gitlab` and the `isotty-gitlab`
skill for each configured agent:

```
isotty-gitlab status
isotty-gitlab fetch [git arguments]
isotty-gitlab push [git arguments]
isotty-gitlab mr list|view|pipelines|create|comment|edit
isotty-gitlab issue list|view|create|comment|edit
isotty-gitlab pipeline list|view|log|retry|cancel
isotty-gitlab job log|retry|cancel
isotty-gitlab project view|edit|protect|unprotect
```

`fetch` and `push` run git with each remote URL rewritten to the relay's
path for that project (`url.<relay>/git/<host>/<path>.git.insteadOf=<url>`)
for that one invocation, so an SSH remote on a custom port works without
touching `.git/config`. The other commands act on the project behind
`origin` unless `--project host/group/project` is given.

## Audit

Pushes and writes are recorded as `gitlab-<operation>` events with the
project key and the merge request or issue `iid`, the pipeline or job id,
or the protected branch name, in the VM event log, as GitHub's are.
