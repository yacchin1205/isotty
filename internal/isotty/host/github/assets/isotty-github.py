#!/usr/bin/env python3
"""Work with the project's GitHub repositories through the IsoTTY relay.

The relay runs on the host inside `isotty attach` and is reachable only
while the host is attached. It forwards git fetch/push and a fixed set of
pull request, issue, workflow run and repository-settings operations for the
repositories pinned in .isotty/github.yaml, with the host's credential; this
VM holds none. Reads, comments, pushes and settings changes each need their
own pin level: read, comment, write, admin.
"""
import argparse
import io
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
import zipfile

RELAY = 'http://127.0.0.1:__ISOTTY_GATEWAY_PORT__/github'
API = RELAY + '/api'
GIT_BASE = RELAY + '/git/'

UNREACHABLE = (
    'isotty-github: the GitHub relay at {relay} is not reachable ({reason}). '
    'It is available only while the host is attached with `isotty attach`; nothing is queued. '
    'Keep your commits and text under /workspace and retry after re-attach.'
)

REMOTE_PATTERNS = [
    re.compile(r'^https://(?:[^@/]+@)?github\.com/([^/]+)/([^/]+?)(?:\.git)?/?$'),
    re.compile(r'^ssh://git@github\.com/([^/]+)/([^/]+?)(?:\.git)?/?$'),
    re.compile(r'^git@github\.com:([^/]+)/([^/]+?)(?:\.git)?/?$'),
]


def api(method, path, params=None, body=None, accept=None, raw=False):
    url = API + path
    if params:
        url += '?' + urllib.parse.urlencode({k: v for k, v in params.items() if v is not None})
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode()
        headers['content-type'] = 'application/json'
    if accept:
        headers['accept'] = accept
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=300) as res:
            content = res.read()
    except urllib.error.HTTPError as e:
        raise SystemExit('isotty-github: ' + describe_http_error(e))
    except urllib.error.URLError as e:
        raise SystemExit(UNREACHABLE.format(relay=RELAY, reason=e.reason))
    if raw:
        return content
    return json.loads(content) if content else None


def describe_http_error(e):
    content = e.read()
    if e.headers.get_content_type() == 'application/json':
        payload = json.loads(content)
        # The relay answers {"error": ...}; GitHub answers {"message": ..., "errors": [...]}.
        if 'error' in payload:
            return payload['error']
        message = payload.get('message', '')
        details = '; '.join(err.get('message') or f"{err.get('field')} {err.get('code')}" for err in payload.get('errors', []))
        return f"GitHub answered {e.code}: {message}" + (f" ({details})" if details else '')
    return f"HTTP {e.code}: {content.decode(errors='replace').strip()}"


def git(*args, check=True):
    return subprocess.run(['git', *args], check=check, capture_output=True, text=True)


def parse_remote_url(url):
    for pattern in REMOTE_PATTERNS:
        m = pattern.match(url)
        if m:
            return f"{m.group(1)}/{m.group(2)}"
    raise SystemExit(f"isotty-github: {url} is not a github.com repository URL")


def resolve_repo(args):
    if args.repo:
        return args.repo
    result = git('remote', 'get-url', 'origin', check=False)
    if result.returncode != 0:
        raise SystemExit('isotty-github: no origin remote in the current directory; pass --repo owner/name')
    return parse_remote_url(result.stdout.strip())


def current_branch():
    return git('rev-parse', '--abbrev-ref', 'HEAD').stdout.strip()


def read_body(args):
    if getattr(args, 'body_file', None):
        with open(args.body_file, encoding='utf-8') as handle:
            return handle.read()
    if sys.stdin.isatty():
        raise SystemExit('isotty-github: the body is read from stdin; redirect a file into the command')
    return sys.stdin.read()


# --- git ---------------------------------------------------------------------

def cmd_git(command, git_args):
    rewrite = []
    for base in ('https://github.com/', 'git@github.com:', 'ssh://git@github.com/'):
        rewrite += ['-c', f'url.{GIT_BASE}.insteadOf={base}']
    env = dict(os.environ, GIT_TERMINAL_PROMPT='0')
    result = subprocess.run(['git', *rewrite, command, *git_args], env=env)
    if result.returncode != 0:
        print('isotty-github: git failed. A "403" means the repository is not pinned for this '
              'project or not pinned as write; `isotty-github status` lists what this VM may reach. '
              'A connection error means the host is not attached.', file=sys.stderr)
    raise SystemExit(result.returncode)


# --- status -------------------------------------------------------------------

def cmd_status(args):
    url = RELAY + '/status'
    try:
        with urllib.request.urlopen(url, timeout=30) as res:
            status = json.load(res)
    except urllib.error.HTTPError as e:
        raise SystemExit('isotty-github: ' + describe_http_error(e))
    except urllib.error.URLError as e:
        raise SystemExit(UNREACHABLE.format(relay=RELAY, reason=e.reason))
    print('GitHub is reachable through the isotty relay. Repositories this VM may use:')
    for repo in sorted(status['repos']):
        print(f"  {repo}\t{status['repos'][repo]}")


# --- pull requests ------------------------------------------------------------

def pr_path(repo, number=None, tail=''):
    return f"/repos/{repo}/pulls" + (f"/{number}" if number is not None else '') + tail


def issue_path(repo, number=None, tail=''):
    return f"/repos/{repo}/issues" + (f"/{number}" if number is not None else '') + tail


def print_item(item, kind):
    draft = ' (draft)' if item.get('draft') else ''
    print(f"#{item['number']} {item['title']}")
    print(f"state: {item['state']}{draft}")
    print(f"author: {item['user']['login']}")
    if kind == 'pr':
        print(f"base: {item['base']['ref']} <- head: {item['head']['label']} ({item['head']['sha'][:12]})")
    if item.get('labels'):
        print('labels: ' + ', '.join(label['name'] for label in item['labels']))
    print(f"url: {item['html_url']}")
    print(f"created: {item['created_at']}  updated: {item['updated_at']}")
    print()
    print(item.get('body') or '(no body)')


def print_comments(repo, number, reviews=False):
    for comment in api('GET', issue_path(repo, number, '/comments'), {'per_page': 100}):
        print()
        print(f"--- comment by {comment['user']['login']} at {comment['created_at']}")
        print(comment['body'])
    if reviews:
        for review in api('GET', pr_path(repo, number, '/reviews'), {'per_page': 100}):
            print()
            print(f"--- review by {review['user']['login']} at {review.get('submitted_at')}: {review['state']}")
            if review.get('body'):
                print(review['body'])
        for comment in api('GET', pr_path(repo, number, '/comments'), {'per_page': 100}):
            print()
            print(f"--- review comment by {comment['user']['login']} on {comment['path']}:{comment.get('line') or comment.get('original_line')}")
            print(comment['body'])


def cmd_pr_list(args):
    repo = resolve_repo(args)
    for pr in api('GET', pr_path(repo), {'state': args.state, 'per_page': 50}):
        draft = ' (draft)' if pr['draft'] else ''
        print(f"#{pr['number']}\t{pr['state']}{draft}\t{pr['head']['ref']}\t{pr['title']}")


def cmd_pr_view(args):
    repo = resolve_repo(args)
    if args.diff:
        sys.stdout.write(api('GET', pr_path(repo, args.number), accept='application/vnd.github.diff', raw=True).decode())
        return
    print_item(api('GET', pr_path(repo, args.number)), 'pr')
    if args.comments:
        print_comments(repo, args.number, reviews=True)


def cmd_pr_checks(args):
    repo = resolve_repo(args)
    sha = api('GET', pr_path(repo, args.number))['head']['sha']
    runs = api('GET', f"/repos/{repo}/commits/{sha}/check-runs", {'per_page': 100})['check_runs']
    if not runs:
        print(f"no check runs for {sha[:12]}")
        return
    for run in runs:
        print(f"{run['name']}\t{run['status']}\t{run.get('conclusion') or '-'}\t{run['html_url']}")


def cmd_pr_create(args):
    repo = resolve_repo(args)
    body = {'title': args.title, 'base': args.base, 'head': args.head or current_branch(), 'body': read_body(args), 'draft': args.draft}
    pr = api('POST', pr_path(repo), body=body)
    print(f"#{pr['number']} {pr['html_url']}")


def cmd_comment(args):
    repo = resolve_repo(args)
    comment = api('POST', issue_path(repo, args.number, '/comments'), body={'body': read_body(args)})
    print(comment['html_url'])


def cmd_pr_edit(args):
    repo = resolve_repo(args)
    body = {}
    if args.title is not None:
        body['title'] = args.title
    if args.body_file is not None:
        body['body'] = read_body(args)
    if not body:
        raise SystemExit('isotty-github: nothing to change; pass --title and/or --body-file')
    print(api('PATCH', pr_path(repo, args.number), body=body)['html_url'])


# --- issues -------------------------------------------------------------------

def cmd_issue_list(args):
    repo = resolve_repo(args)
    params = {'state': args.state, 'per_page': 50, 'labels': ','.join(args.label) if args.label else None}
    for issue in api('GET', issue_path(repo), params):
        if 'pull_request' in issue:
            continue
        labels = ','.join(label['name'] for label in issue['labels'])
        print(f"#{issue['number']}\t{issue['state']}\t{labels}\t{issue['title']}")


def cmd_issue_view(args):
    repo = resolve_repo(args)
    print_item(api('GET', issue_path(repo, args.number)), 'issue')
    if args.comments:
        print_comments(repo, args.number)


def cmd_issue_create(args):
    repo = resolve_repo(args)
    body = {'title': args.title, 'body': read_body(args)}
    if args.label:
        body['labels'] = args.label
    issue = api('POST', issue_path(repo), body=body)
    print(f"#{issue['number']} {issue['html_url']}")


def cmd_issue_edit(args):
    repo = resolve_repo(args)
    body = {}
    if args.title is not None:
        body['title'] = args.title
    if args.body_file is not None:
        body['body'] = read_body(args)
    if args.label:
        body['labels'] = args.label
    if not body:
        raise SystemExit('isotty-github: nothing to change; pass --title, --body-file and/or --label')
    print(api('PATCH', issue_path(repo, args.number), body=body)['html_url'])


# --- workflow runs ------------------------------------------------------------

def run_path(repo, run_id=None, tail=''):
    return f"/repos/{repo}/actions/runs" + (f"/{run_id}" if run_id is not None else '') + tail


def cmd_run_list(args):
    repo = resolve_repo(args)
    runs = api('GET', run_path(repo), {'branch': args.branch, 'per_page': 50})['workflow_runs']
    for run in runs:
        if args.workflow and args.workflow not in (run['name'], os.path.basename(run['path'])):
            continue
        outcome = run['status'] + (f"/{run['conclusion']}" if run.get('conclusion') else '')
        print(f"{run['id']}\t{outcome}\t{run['name']}\t{run['head_branch']}\t{run['event']}\t{run['created_at']}")


def cmd_run_view(args):
    repo = resolve_repo(args)
    run = api('GET', run_path(repo, args.id))
    print(f"run {run['id']}: {run['name']} ({os.path.basename(run['path'])})")
    print(f"status: {run['status']}" + (f" / {run['conclusion']}" if run.get('conclusion') else ''))
    print(f"branch: {run['head_branch']}  commit: {run['head_sha'][:12]}  event: {run['event']}")
    print(f"url: {run['html_url']}")
    print(f"created: {run['created_at']}  updated: {run['updated_at']}")
    print()
    for job in api('GET', run_path(repo, args.id, '/jobs'), {'per_page': 100})['jobs']:
        outcome = job['status'] + (f"/{job['conclusion']}" if job.get('conclusion') else '')
        print(f"job {job['id']}\t{outcome}\t{job['name']}")
        for step in job.get('steps', []):
            if step.get('conclusion') not in (None, 'success', 'skipped'):
                print(f"    step {step['number']} {step['name']}: {step['conclusion']}")


def print_job_log(repo, job_id):
    sys.stdout.write(api('GET', f"/repos/{repo}/actions/jobs/{job_id}/logs", raw=True).decode(errors='replace'))


def cmd_run_log(args):
    repo = resolve_repo(args)
    if args.job:
        print_job_log(repo, args.job)
        return
    for job in api('GET', run_path(repo, args.id, '/jobs'), {'per_page': 100})['jobs']:
        print(f"===== job {job['id']} {job['name']} ({job['status']}{'/' + job['conclusion'] if job.get('conclusion') else ''})")
        if job['status'] != 'completed':
            print('(log is available once the job completes)')
            continue
        print_job_log(repo, job['id'])


def cmd_run_download(args):
    repo = resolve_repo(args)
    artifacts = api('GET', run_path(repo, args.id, '/artifacts'), {'per_page': 100})['artifacts']
    if not artifacts:
        print(f"run {args.id} has no artifacts")
        return
    for artifact in artifacts:
        if artifact['expired']:
            print(f"{artifact['name']}: expired, skipped")
            continue
        target = os.path.join(args.dir, artifact['name'])
        os.makedirs(target, exist_ok=True)
        content = api('GET', f"/repos/{repo}/actions/artifacts/{artifact['id']}/zip", raw=True)
        with zipfile.ZipFile(io.BytesIO(content)) as archive:
            archive.extractall(target)
        print(f"{artifact['name']}: {target}")


def cmd_run_rerun(args):
    repo = resolve_repo(args)
    tail = '/rerun-failed-jobs' if args.failed else '/rerun'
    api('POST', run_path(repo, args.id, tail))
    print(f"run {args.id}: rerun requested" + (' (failed jobs only)' if args.failed else ''))


def cmd_run_cancel(args):
    repo = resolve_repo(args)
    api('POST', run_path(repo, args.id, '/cancel'))
    print(f"run {args.id}: cancel requested")


# --- repository settings ------------------------------------------------------

def optional(path):
    """GET that reports 404 (not configured) and 403 (GitHub's reason, e.g. plan limits) instead of failing."""
    req = urllib.request.Request(API + path)
    try:
        with urllib.request.urlopen(req, timeout=60) as res:
            return json.load(res), None
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return None, 'none'
        if e.code == 403:
            return None, describe_http_error(e)
        raise SystemExit('isotty-github: ' + describe_http_error(e))
    except urllib.error.URLError as e:
        raise SystemExit(UNREACHABLE.format(relay=RELAY, reason=e.reason))


def cmd_repo_view(args):
    repo = resolve_repo(args)
    info = api('GET', f"/repos/{repo}")
    default_branch = info['default_branch']
    print(f"{info['full_name']}: {info['visibility']}" + (', archived' if info['archived'] else ''))
    print(f"default branch: {default_branch}")
    print(f"features: issues={info['has_issues']} wiki={info['has_wiki']} projects={info['has_projects']} discussions={info.get('has_discussions')}")
    # GitHub tells the merge settings to those who can push, and leaves
    # them out of the answer for everyone else.
    if 'allow_merge_commit' in info:
        print(f"merge: merge_commit={info['allow_merge_commit']} squash={info['allow_squash_merge']} rebase={info['allow_rebase_merge']} "
              f"auto_merge={info.get('allow_auto_merge')} delete_branch_on_merge={info['delete_branch_on_merge']}")
    else:
        print('merge: not shown (GitHub tells the merge settings to those who can push)')

    print()
    print(f"branch protection on {default_branch}:")
    protection, why = optional(f"/repos/{repo}/branches/{urllib.parse.quote(default_branch, safe='')}/protection")
    if protection is None:
        print(f"  {why}")
    else:
        checks = protection.get('required_status_checks')
        reviews = protection.get('required_pull_request_reviews')
        print(f"  required status checks: {', '.join(checks['contexts']) if checks and checks['contexts'] else 'none'}"
              + (f" (strict={checks['strict']})" if checks else ''))
        print(f"  required reviews: {reviews['required_approving_review_count']} approving"
              f", code owners={reviews['require_code_owner_reviews']}" if reviews else '  required reviews: none')
        print(f"  enforce for admins: {protection['enforce_admins']['enabled']}")
        print(f"  force pushes: {protection['allow_force_pushes']['enabled']}  deletions: {protection['allow_deletions']['enabled']}")
    rules, why = optional(f"/repos/{repo}/rules/branches/{urllib.parse.quote(default_branch, safe='')}")
    if rules is None:
        print(f"  rulesets: {why}")
    else:
        print('  rulesets: ' + (', '.join(sorted({rule['type'] for rule in rules})) if rules else 'none'))

    print()
    permissions, why = optional(f"/repos/{repo}/actions/permissions")
    if permissions is None:
        print(f"actions: {why}")
    else:
        print(f"actions: enabled={permissions['enabled']} allowed_actions={permissions.get('allowed_actions', '-')}")

    environments, why = optional(f"/repos/{repo}/environments")
    if environments is None:
        print(f"environments: {why}")
    else:
        names = []
        for env in environments.get('environments', []):
            rules = ', '.join(rule['type'] for rule in env.get('protection_rules', []))
            names.append(env['name'] + (f" [{rules}]" if rules else ''))
        print('environments: ' + (', '.join(names) if names else 'none'))

    pages, why = optional(f"/repos/{repo}/pages")
    print('pages: ' + (f"{pages['html_url']} ({pages['status']})" if pages else why))

    secrets, why = optional(f"/repos/{repo}/actions/secrets")
    if secrets is None:
        print(f"actions secrets: {why}")
    else:
        print('actions secrets (names only): ' + (', '.join(s['name'] for s in secrets['secrets']) if secrets['secrets'] else 'none'))


# --- repository settings: changes (admin) -------------------------------------

def parse_settings(pairs):
    """--set name=value pairs; a value that is JSON (true, 3, ["a"]) is taken as such, anything else as a string."""
    body = {}
    for pair in pairs:
        name, sep, value = pair.partition('=')
        if not sep or not name:
            raise SystemExit(f"isotty-github: --set expects name=value, got {pair!r}")
        try:
            body[name] = json.loads(value)
        except ValueError:
            body[name] = value
    return body


def read_json_body(path):
    with open(path, encoding='utf-8') as handle:
        return json.load(handle)


def on_off(value):
    return value == 'on'


def cmd_repo_edit(args):
    repo = resolve_repo(args)
    info = api('PATCH', f"/repos/{repo}", body=parse_settings(args.set))
    print(f"{info['full_name']} updated")


def cmd_repo_topics(args):
    repo = resolve_repo(args)
    result = api('PUT', f"/repos/{repo}/topics", body={'names': args.name})
    print('topics: ' + (', '.join(result['names']) if result['names'] else 'none'))


def cmd_repo_protect(args):
    repo = resolve_repo(args)
    if args.body_file:
        body = read_json_body(args.body_file)
    else:
        reviews = None
        if args.require_reviews is not None:
            reviews = {'required_approving_review_count': args.require_reviews,
                       'require_code_owner_reviews': args.require_codeowners,
                       'dismiss_stale_reviews': args.dismiss_stale}
        checks = None
        if args.require_checks:
            checks = {'strict': args.strict, 'contexts': args.require_checks.split(',')}
        body = {'required_status_checks': checks, 'enforce_admins': args.enforce_admins,
                'required_pull_request_reviews': reviews, 'restrictions': None,
                'required_linear_history': args.linear_history, 'allow_force_pushes': args.allow_force_pushes,
                'allow_deletions': args.allow_deletions,
                'required_conversation_resolution': args.require_conversation_resolution, 'lock_branch': args.lock}
    api('PUT', f"/repos/{repo}/branches/{urllib.parse.quote(args.branch, safe='')}/protection", body=body)
    print(f"{args.branch}: protection set")


def cmd_repo_unprotect(args):
    repo = resolve_repo(args)
    api('DELETE', f"/repos/{repo}/branches/{urllib.parse.quote(args.branch, safe='')}/protection")
    print(f"{args.branch}: protection removed")


def cmd_ruleset_list(args):
    repo = resolve_repo(args)
    for ruleset in api('GET', f"/repos/{repo}/rulesets", {'per_page': 100}):
        print(f"{ruleset['id']}\t{ruleset['enforcement']}\t{ruleset['target']}\t{ruleset['name']}")


def cmd_ruleset_view(args):
    repo = resolve_repo(args)
    print(json.dumps(api('GET', f"/repos/{repo}/rulesets/{args.id}"), indent=2))


def cmd_ruleset_create(args):
    repo = resolve_repo(args)
    ruleset = api('POST', f"/repos/{repo}/rulesets", body=read_json_body(args.body_file))
    print(f"ruleset {ruleset['id']} {ruleset['name']} created")


def cmd_ruleset_edit(args):
    repo = resolve_repo(args)
    ruleset = api('PUT', f"/repos/{repo}/rulesets/{args.id}", body=read_json_body(args.body_file))
    print(f"ruleset {ruleset['id']} {ruleset['name']} updated")


def cmd_ruleset_delete(args):
    repo = resolve_repo(args)
    api('DELETE', f"/repos/{repo}/rulesets/{args.id}")
    print(f"ruleset {args.id} deleted")


def cmd_repo_actions(args):
    repo = resolve_repo(args)
    changed = False
    if args.enabled is not None or args.allowed is not None:
        current = api('GET', f"/repos/{repo}/actions/permissions")
        body = {'enabled': on_off(args.enabled) if args.enabled is not None else current['enabled']}
        if args.allowed is not None:
            body['allowed_actions'] = args.allowed
        api('PUT', f"/repos/{repo}/actions/permissions", body=body)
        changed = True
    if args.workflow_permissions is not None or args.can_approve_prs is not None:
        body = {}
        if args.workflow_permissions is not None:
            body['default_workflow_permissions'] = args.workflow_permissions
        if args.can_approve_prs is not None:
            body['can_approve_pull_request_reviews'] = on_off(args.can_approve_prs)
        api('PUT', f"/repos/{repo}/actions/permissions/workflow", body=body)
        changed = True
    if not changed:
        raise SystemExit('isotty-github: nothing to change; see --help for the flags')
    permissions = api('GET', f"/repos/{repo}/actions/permissions")
    workflow = api('GET', f"/repos/{repo}/actions/permissions/workflow")
    print(f"actions: enabled={permissions['enabled']} allowed_actions={permissions.get('allowed_actions', '-')} "
          f"workflow_permissions={workflow['default_workflow_permissions']} "
          f"can_approve_pull_request_reviews={workflow['can_approve_pull_request_reviews']}")


def cmd_env_set(args):
    repo = resolve_repo(args)
    if args.body_file:
        body = read_json_body(args.body_file)
    else:
        body = {}
        if args.wait_timer is not None:
            body['wait_timer'] = args.wait_timer
        if args.prevent_self_review is not None:
            body['prevent_self_review'] = on_off(args.prevent_self_review)
        if args.protected_branches_only is not None:
            body['deployment_branch_policy'] = {'protected_branches': on_off(args.protected_branches_only), 'custom_branch_policies': not on_off(args.protected_branches_only)}
    env = api('PUT', f"/repos/{repo}/environments/{urllib.parse.quote(args.name, safe='')}", body=body)
    rules = ', '.join(rule['type'] for rule in env.get('protection_rules', []))
    print(f"environment {env['name']}" + (f" [{rules}]" if rules else ' (no protection rules)'))


def cmd_env_delete(args):
    repo = resolve_repo(args)
    api('DELETE', f"/repos/{repo}/environments/{urllib.parse.quote(args.name, safe='')}")
    print(f"environment {args.name} deleted")


def cmd_pages_enable(args):
    repo = resolve_repo(args)
    if args.workflow:
        body = {'build_type': 'workflow'}
    elif args.branch:
        body = {'build_type': 'legacy', 'source': {'branch': args.branch, 'path': args.path}}
    else:
        raise SystemExit('isotty-github: pass --branch <branch> [--path /docs] or --workflow')
    pages = api('POST', f"/repos/{repo}/pages", body=body)
    print(f"pages: {pages['html_url']} ({pages['status'] or 'building'})")


def cmd_pages_edit(args):
    repo = resolve_repo(args)
    api('PUT', f"/repos/{repo}/pages", body=parse_settings(args.set))
    pages = api('GET', f"/repos/{repo}/pages")
    print(f"pages: {pages['html_url']} ({pages['status']}) https_enforced={pages.get('https_enforced')} cname={pages.get('cname')}")


def cmd_pages_disable(args):
    repo = resolve_repo(args)
    api('DELETE', f"/repos/{repo}/pages")
    print('pages disabled')


# --- argument parsing ---------------------------------------------------------

def main():
    # git's own options must reach git untouched, so fetch/push bypass argparse.
    if len(sys.argv) > 1 and sys.argv[1] in ('fetch', 'push'):
        cmd_git(sys.argv[1], sys.argv[2:])
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command', required=True)

    p = sub.add_parser('status', help='check that the relay is reachable and list the pinned repositories')
    p.set_defaults(func=cmd_status)

    for name in ('fetch', 'push'):
        sub.add_parser(name, help=f'run `git {name}` through the relay; every following argument goes to git')

    repo_parent = argparse.ArgumentParser(add_help=False)
    repo_parent.add_argument('--repo', help='owner/name; defaults to the repository behind origin')

    pr = sub.add_parser('pr', help='pull requests').add_subparsers(dest='pr_command', required=True)
    p = pr.add_parser('list', parents=[repo_parent])
    p.add_argument('--state', choices=['open', 'closed', 'all'], default='open')
    p.set_defaults(func=cmd_pr_list)
    p = pr.add_parser('view', parents=[repo_parent])
    p.add_argument('number', type=int)
    p.add_argument('--diff', action='store_true', help='print the diff instead of the description')
    p.add_argument('--comments', action='store_true', help='include comments and reviews')
    p.set_defaults(func=cmd_pr_view)
    p = pr.add_parser('checks', parents=[repo_parent])
    p.add_argument('number', type=int)
    p.set_defaults(func=cmd_pr_checks)
    p = pr.add_parser('create', parents=[repo_parent], help='create a pull request; the description is read from stdin')
    p.add_argument('--title', required=True)
    p.add_argument('--base', required=True)
    p.add_argument('--head', help='defaults to the current branch')
    p.add_argument('--draft', action='store_true')
    p.set_defaults(func=cmd_pr_create)
    p = pr.add_parser('comment', parents=[repo_parent], help='comment on a pull request; the text is read from stdin')
    p.add_argument('number', type=int)
    p.set_defaults(func=cmd_comment)
    p = pr.add_parser('edit', parents=[repo_parent])
    p.add_argument('number', type=int)
    p.add_argument('--title')
    p.add_argument('--body-file')
    p.set_defaults(func=cmd_pr_edit)

    issue = sub.add_parser('issue', help='issues').add_subparsers(dest='issue_command', required=True)
    p = issue.add_parser('list', parents=[repo_parent])
    p.add_argument('--state', choices=['open', 'closed', 'all'], default='open')
    p.add_argument('--label', action='append')
    p.set_defaults(func=cmd_issue_list)
    p = issue.add_parser('view', parents=[repo_parent])
    p.add_argument('number', type=int)
    p.add_argument('--comments', action='store_true')
    p.set_defaults(func=cmd_issue_view)
    p = issue.add_parser('create', parents=[repo_parent], help='create an issue; the body is read from stdin')
    p.add_argument('--title', required=True)
    p.add_argument('--label', action='append')
    p.set_defaults(func=cmd_issue_create)
    p = issue.add_parser('comment', parents=[repo_parent], help='comment on an issue; the text is read from stdin')
    p.add_argument('number', type=int)
    p.set_defaults(func=cmd_comment)
    p = issue.add_parser('edit', parents=[repo_parent])
    p.add_argument('number', type=int)
    p.add_argument('--title')
    p.add_argument('--body-file')
    p.add_argument('--label', action='append', help='replaces the label set')
    p.set_defaults(func=cmd_issue_edit)

    run = sub.add_parser('run', help='workflow runs').add_subparsers(dest='run_command', required=True)
    p = run.add_parser('list', parents=[repo_parent])
    p.add_argument('--branch')
    p.add_argument('--workflow', help='workflow name or file name')
    p.set_defaults(func=cmd_run_list)
    p = run.add_parser('view', parents=[repo_parent])
    p.add_argument('id', type=int)
    p.set_defaults(func=cmd_run_view)
    p = run.add_parser('log', parents=[repo_parent], help='print the job logs of a run')
    p.add_argument('id', type=int)
    p.add_argument('--job', type=int, help='one job only')
    p.set_defaults(func=cmd_run_log)
    p = run.add_parser('download', parents=[repo_parent], help='download and unpack the artifacts of a run')
    p.add_argument('id', type=int)
    p.add_argument('--dir', default='.')
    p.set_defaults(func=cmd_run_download)
    p = run.add_parser('rerun', parents=[repo_parent])
    p.add_argument('id', type=int)
    p.add_argument('--failed', action='store_true', help='rerun the failed jobs only')
    p.set_defaults(func=cmd_run_rerun)
    p = run.add_parser('cancel', parents=[repo_parent])
    p.add_argument('id', type=int)
    p.set_defaults(func=cmd_run_cancel)

    repo = sub.add_parser('repo', help='repository settings; changes need the admin level').add_subparsers(dest='repo_command', required=True)
    p = repo.add_parser('view', parents=[repo_parent], help='show the settings an agent needs to work within')
    p.set_defaults(func=cmd_repo_view)
    p = repo.add_parser('edit', parents=[repo_parent], help='change repository settings, e.g. --set delete_branch_on_merge=true --set description="..."')
    p.add_argument('--set', action='append', required=True, metavar='FIELD=VALUE',
                   help='a field of the repository (description, homepage, default_branch, has_issues, has_wiki, has_projects, '
                        'has_discussions, allow_merge_commit, allow_squash_merge, allow_rebase_merge, allow_auto_merge, '
                        'allow_update_branch, delete_branch_on_merge, merge_commit_title, merge_commit_message, '
                        'squash_merge_commit_title, squash_merge_commit_message, web_commit_signoff_required); repeatable')
    p.set_defaults(func=cmd_repo_edit)
    p = repo.add_parser('topics', parents=[repo_parent], help='replace the topics')
    p.add_argument('name', nargs='*')
    p.set_defaults(func=cmd_repo_topics)
    p = repo.add_parser('protect', parents=[repo_parent], help='set the classic branch protection of a branch to exactly what the flags say')
    p.add_argument('branch')
    p.add_argument('--require-reviews', type=int, metavar='N', help='require N approving reviews')
    p.add_argument('--require-codeowners', action='store_true')
    p.add_argument('--dismiss-stale', action='store_true', help='dismiss approvals when new commits are pushed')
    p.add_argument('--require-checks', metavar='CTX,CTX', help='required status check contexts')
    p.add_argument('--strict', action='store_true', help='require the branch to be up to date before merging')
    p.add_argument('--enforce-admins', action='store_true')
    p.add_argument('--linear-history', action='store_true')
    p.add_argument('--allow-force-pushes', action='store_true')
    p.add_argument('--allow-deletions', action='store_true')
    p.add_argument('--require-conversation-resolution', action='store_true')
    p.add_argument('--lock', action='store_true', help='make the branch read-only')
    p.add_argument('--body-file', help='send this JSON as the protection instead of the flags')
    p.set_defaults(func=cmd_repo_protect)
    p = repo.add_parser('unprotect', parents=[repo_parent], help='remove the classic branch protection of a branch')
    p.add_argument('branch')
    p.set_defaults(func=cmd_repo_unprotect)

    ruleset = repo.add_parser('ruleset', help='repository rulesets').add_subparsers(dest='ruleset_command', required=True)
    p = ruleset.add_parser('list', parents=[repo_parent])
    p.set_defaults(func=cmd_ruleset_list)
    p = ruleset.add_parser('view', parents=[repo_parent])
    p.add_argument('id', type=int)
    p.set_defaults(func=cmd_ruleset_view)
    p = ruleset.add_parser('create', parents=[repo_parent], help='create a ruleset from a JSON file (name, target, enforcement, conditions, rules, bypass_actors)')
    p.add_argument('--body-file', required=True)
    p.set_defaults(func=cmd_ruleset_create)
    p = ruleset.add_parser('edit', parents=[repo_parent], help='replace the fields of a ruleset from a JSON file')
    p.add_argument('id', type=int)
    p.add_argument('--body-file', required=True)
    p.set_defaults(func=cmd_ruleset_edit)
    p = ruleset.add_parser('delete', parents=[repo_parent])
    p.add_argument('id', type=int)
    p.set_defaults(func=cmd_ruleset_delete)

    p = repo.add_parser('actions', parents=[repo_parent], help='change the Actions settings')
    p.add_argument('--enabled', choices=['on', 'off'])
    p.add_argument('--allowed', choices=['all', 'local_only', 'selected'], help='which actions may run')
    p.add_argument('--workflow-permissions', choices=['read', 'write'], help='default GITHUB_TOKEN permissions')
    p.add_argument('--can-approve-prs', choices=['on', 'off'], help='whether workflows may approve pull requests')
    p.set_defaults(func=cmd_repo_actions)

    env = repo.add_parser('env', help='deployment environments').add_subparsers(dest='env_command', required=True)
    p = env.add_parser('set', parents=[repo_parent], help='create an environment or change its protection rules')
    p.add_argument('name')
    p.add_argument('--wait-timer', type=int, metavar='MINUTES')
    p.add_argument('--prevent-self-review', choices=['on', 'off'])
    p.add_argument('--protected-branches-only', choices=['on', 'off'], help='deploy only from protected branches')
    p.add_argument('--body-file', help='send this JSON instead of the flags (wait_timer, prevent_self_review, reviewers, deployment_branch_policy)')
    p.set_defaults(func=cmd_env_set)
    p = env.add_parser('delete', parents=[repo_parent])
    p.add_argument('name')
    p.set_defaults(func=cmd_env_delete)

    pages = repo.add_parser('pages', help='GitHub Pages').add_subparsers(dest='pages_command', required=True)
    p = pages.add_parser('enable', parents=[repo_parent])
    p.add_argument('--branch', help='publish from this branch')
    p.add_argument('--path', choices=['/', '/docs'], default='/')
    p.add_argument('--workflow', action='store_true', help='publish from an Actions workflow instead')
    p.set_defaults(func=cmd_pages_enable)
    p = pages.add_parser('edit', parents=[repo_parent], help='change the Pages settings, e.g. --set https_enforced=true')
    p.add_argument('--set', action='append', required=True, metavar='FIELD=VALUE', help='cname, https_enforced, build_type or source; repeatable')
    p.set_defaults(func=cmd_pages_edit)
    p = pages.add_parser('disable', parents=[repo_parent])
    p.set_defaults(func=cmd_pages_disable)

    args = parser.parse_args()
    args.func(args)


if __name__ == '__main__':
    main()
