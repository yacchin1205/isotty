#!/usr/bin/env python3
"""Work with the project's GitLab projects through the IsoTTY relay.

The relay runs on the host inside `isotty attach` and is reachable only
while the host is attached. It forwards git fetch/push and a fixed set of
merge request, issue, pipeline, job and project-settings operations for the
projects pinned in .isotty/gitlab.yaml, with the host's token for each
instance; this VM holds none. Reads, comments, pushes and settings changes
each need their own pin level: read, comment, write, admin.
"""
import argparse
import json
import os
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request

RELAY = 'http://127.0.0.1:__ISOTTY_GATEWAY_PORT__/gitlab'
API = RELAY + '/api'
GIT_BASE = RELAY + '/git/'

UNREACHABLE = (
    'isotty-gitlab: the GitLab relay at {relay} is not reachable ({reason}). '
    'It is available only while the host is attached with `isotty attach`; nothing is queued. '
    'Keep your commits and text under /workspace and retry after re-attach.'
)

# A project is named host/group/project: the instance's host name and the
# project path, as the pin spells it. The port of an SSH remote is dropped.
REMOTE_PATTERNS = [
    re.compile(r'^https?://(?:[^@/]+@)?([^/:]+)(?::[0-9]+)?/(.+?)(?:\.git)?/?$'),
    re.compile(r'^ssh://(?:[^@/]+@)?([^/:]+)(?::[0-9]+)?/(.+?)(?:\.git)?/?$'),
    re.compile(r'^(?:[^@/:]+@)?([^/:]+):([^/].*?)(?:\.git)?/?$'),
]


def api(method, path, params=None, body=None, raw=False):
    url = API + path
    if params:
        url += '?' + urllib.parse.urlencode({k: v for k, v in params.items() if v is not None})
    data = None
    headers = {}
    if body is not None:
        data = json.dumps(body).encode()
        headers['content-type'] = 'application/json'
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=300) as res:
            content = res.read()
    except urllib.error.HTTPError as e:
        raise SystemExit('isotty-gitlab: ' + describe_http_error(e))
    except urllib.error.URLError as e:
        raise SystemExit(UNREACHABLE.format(relay=RELAY, reason=e.reason))
    if raw:
        return content
    return json.loads(content) if content else None


def describe_http_error(e):
    content = e.read()
    if e.headers.get_content_type() == 'application/json':
        payload = json.loads(content)
        # The relay answers {"error": ...}; GitLab answers {"message": ...} or {"error": ...}.
        if isinstance(payload, dict) and 'error' in payload:
            return str(payload['error'])
        if isinstance(payload, dict) and 'message' in payload:
            return f"GitLab answered {e.code}: {payload['message']}"
    return f"HTTP {e.code}: {content.decode(errors='replace').strip()}"


def git(*args, check=True):
    return subprocess.run(['git', *args], check=check, capture_output=True, text=True)


def parse_remote_url(url):
    for pattern in REMOTE_PATTERNS:
        m = pattern.match(url)
        if m and '/' in m.group(2):
            return f"{m.group(1)}/{m.group(2)}"
    return None


def resolve_project(args):
    if args.project:
        return args.project
    result = git('remote', 'get-url', 'origin', check=False)
    if result.returncode != 0:
        raise SystemExit('isotty-gitlab: no origin remote in the current directory; pass --project host/group/project')
    project = parse_remote_url(result.stdout.strip())
    if project is None:
        raise SystemExit(f"isotty-gitlab: origin ({result.stdout.strip()}) is not a URL with a host and a project path; pass --project host/group/project")
    return project


def project_path(project, tail=''):
    host, _, path = project.partition('/')
    return f"/{host}/projects/{urllib.parse.quote(path, safe='')}" + tail


def current_branch():
    return git('rev-parse', '--abbrev-ref', 'HEAD').stdout.strip()


def read_body(args):
    if getattr(args, 'body_file', None):
        with open(args.body_file, encoding='utf-8') as handle:
            return handle.read()
    if sys.stdin.isatty():
        raise SystemExit('isotty-gitlab: the body is read from stdin; redirect a file into the command')
    return sys.stdin.read()


# --- git ---------------------------------------------------------------------

def cmd_git(command, git_args):
    # Every remote URL that names a project, and any URL given on the command
    # line, is rewritten to the relay's path for that project; the remotes
    # themselves stay as they are.
    urls = set(arg for arg in git_args if parse_remote_url(arg))
    remotes = git('remote', check=False)
    for remote in remotes.stdout.split():
        urls.add(git('remote', 'get-url', remote).stdout.strip())
    rewrite = []
    for url in sorted(urls):
        project = parse_remote_url(url)
        if project:
            rewrite += ['-c', f'url.{GIT_BASE}{project}.git.insteadOf={url}']
    env = dict(os.environ, GIT_TERMINAL_PROMPT='0')
    result = subprocess.run(['git', *rewrite, command, *git_args], env=env)
    if result.returncode != 0:
        print('isotty-gitlab: git failed. A "403" means the project is not pinned for this '
              'project or not pinned as write; `isotty-gitlab status` lists what this VM may reach. '
              'A connection error means the host is not attached.', file=sys.stderr)
    raise SystemExit(result.returncode)


# --- status -------------------------------------------------------------------

def cmd_status(args):
    url = RELAY + '/status'
    try:
        with urllib.request.urlopen(url, timeout=30) as res:
            status = json.load(res)
    except urllib.error.HTTPError as e:
        raise SystemExit('isotty-gitlab: ' + describe_http_error(e))
    except urllib.error.URLError as e:
        raise SystemExit(UNREACHABLE.format(relay=RELAY, reason=e.reason))
    print('GitLab is reachable through the isotty relay. Projects this VM may use:')
    for project in sorted(status['repos']):
        print(f"  {project}\t{status['repos'][project]}")


# --- merge requests and issues ------------------------------------------------

def mr_path(project, iid=None, tail=''):
    return project_path(project, '/merge_requests' + (f"/{iid}" if iid is not None else '') + tail)


def issue_path(project, iid=None, tail=''):
    return project_path(project, '/issues' + (f"/{iid}" if iid is not None else '') + tail)


def print_item(item, kind):
    draft = ' (draft)' if item.get('draft') else ''
    print(f"!{item['iid']} {item['title']}" if kind == 'mr' else f"#{item['iid']} {item['title']}")
    print(f"state: {item['state']}{draft}")
    print(f"author: {item['author']['username']}")
    if kind == 'mr':
        print(f"target: {item['target_branch']} <- source: {item['source_branch']} ({(item.get('sha') or '')[:12]})")
    if item.get('labels'):
        print('labels: ' + ', '.join(item['labels']))
    print(f"url: {item['web_url']}")
    print(f"created: {item['created_at']}  updated: {item['updated_at']}")
    print()
    print(item.get('description') or '(no description)')


def print_notes(path):
    for note in api('GET', path, {'per_page': 100, 'sort': 'asc'}):
        if note.get('system'):
            continue
        print()
        print(f"--- note by {note['author']['username']} at {note['created_at']}")
        print(note['body'])


def cmd_mr_list(args):
    project = resolve_project(args)
    for mr in api('GET', mr_path(project), {'state': args.state, 'per_page': 50}):
        draft = ' (draft)' if mr.get('draft') else ''
        print(f"!{mr['iid']}\t{mr['state']}{draft}\t{mr['source_branch']}\t{mr['title']}")


def cmd_mr_view(args):
    project = resolve_project(args)
    if args.diff:
        for change in api('GET', mr_path(project, args.iid, '/changes'))['changes']:
            print(f"diff --git a/{change['old_path']} b/{change['new_path']}")
            sys.stdout.write(change['diff'])
        return
    print_item(api('GET', mr_path(project, args.iid)), 'mr')
    if args.comments:
        print_notes(mr_path(project, args.iid, '/notes'))


def cmd_mr_pipelines(args):
    project = resolve_project(args)
    pipelines = api('GET', mr_path(project, args.iid, '/pipelines'))
    if not pipelines:
        print(f"no pipelines for !{args.iid}")
        return
    for pipeline in pipelines:
        print(f"{pipeline['id']}\t{pipeline['status']}\t{pipeline['sha'][:12]}\t{pipeline['web_url']}")


def cmd_mr_create(args):
    project = resolve_project(args)
    body = {'title': args.title, 'target_branch': args.target, 'source_branch': args.source or current_branch(), 'description': read_body(args)}
    mr = api('POST', mr_path(project), body=body)
    print(f"!{mr['iid']} {mr['web_url']}")


def cmd_mr_comment(args):
    project = resolve_project(args)
    api('POST', mr_path(project, args.iid, '/notes'), body={'body': read_body(args)})
    print(f"commented on !{args.iid}")


def cmd_mr_edit(args):
    project = resolve_project(args)
    body = {}
    if args.title is not None:
        body['title'] = args.title
    if args.body_file is not None:
        body['description'] = read_body(args)
    if not body:
        raise SystemExit('isotty-gitlab: nothing to change; pass --title and/or --body-file')
    print(api('PUT', mr_path(project, args.iid), body=body)['web_url'])


def cmd_issue_list(args):
    project = resolve_project(args)
    params = {'state': args.state, 'per_page': 50, 'labels': ','.join(args.label) if args.label else None}
    for issue in api('GET', issue_path(project), params):
        print(f"#{issue['iid']}\t{issue['state']}\t{','.join(issue['labels'])}\t{issue['title']}")


def cmd_issue_view(args):
    project = resolve_project(args)
    print_item(api('GET', issue_path(project, args.iid)), 'issue')
    if args.comments:
        print_notes(issue_path(project, args.iid, '/notes'))


def cmd_issue_create(args):
    project = resolve_project(args)
    body = {'title': args.title, 'description': read_body(args)}
    if args.label:
        body['labels'] = ','.join(args.label)
    issue = api('POST', issue_path(project), body=body)
    print(f"#{issue['iid']} {issue['web_url']}")


def cmd_issue_comment(args):
    project = resolve_project(args)
    api('POST', issue_path(project, args.iid, '/notes'), body={'body': read_body(args)})
    print(f"commented on #{args.iid}")


def cmd_issue_edit(args):
    project = resolve_project(args)
    body = {}
    if args.title is not None:
        body['title'] = args.title
    if args.body_file is not None:
        body['description'] = read_body(args)
    if args.label:
        body['labels'] = ','.join(args.label)
    if not body:
        raise SystemExit('isotty-gitlab: nothing to change; pass --title, --body-file and/or --label')
    print(api('PUT', issue_path(project, args.iid), body=body)['web_url'])


# --- pipelines and jobs -------------------------------------------------------

def pipeline_path(project, pipeline_id=None, tail=''):
    return project_path(project, '/pipelines' + (f"/{pipeline_id}" if pipeline_id is not None else '') + tail)


def job_path(project, job_id, tail=''):
    return project_path(project, f"/jobs/{job_id}" + tail)


def cmd_pipeline_list(args):
    project = resolve_project(args)
    for pipeline in api('GET', pipeline_path(project), {'ref': args.ref, 'per_page': 50}):
        print(f"{pipeline['id']}\t{pipeline['status']}\t{pipeline['ref']}\t{pipeline['sha'][:12]}\t{pipeline.get('source', '-')}\t{pipeline['created_at']}")


def cmd_pipeline_view(args):
    project = resolve_project(args)
    pipeline = api('GET', pipeline_path(project, args.id))
    print(f"pipeline {pipeline['id']}: {pipeline['status']}")
    print(f"ref: {pipeline['ref']}  commit: {pipeline['sha'][:12]}  source: {pipeline.get('source', '-')}")
    print(f"url: {pipeline['web_url']}")
    print(f"created: {pipeline['created_at']}  updated: {pipeline['updated_at']}")
    print()
    for job in api('GET', pipeline_path(project, args.id, '/jobs'), {'per_page': 100}):
        print(f"job {job['id']}\t{job['status']}\t{job['stage']}\t{job['name']}")


def print_job_trace(project, job_id):
    sys.stdout.write(api('GET', job_path(project, job_id, '/trace'), raw=True).decode(errors='replace'))


def cmd_pipeline_log(args):
    project = resolve_project(args)
    for job in api('GET', pipeline_path(project, args.id, '/jobs'), {'per_page': 100}):
        print(f"===== job {job['id']} {job['name']} ({job['status']})")
        print_job_trace(project, job['id'])


def cmd_pipeline_retry(args):
    project = resolve_project(args)
    api('POST', pipeline_path(project, args.id, '/retry'))
    print(f"pipeline {args.id}: retry requested")


def cmd_pipeline_cancel(args):
    project = resolve_project(args)
    api('POST', pipeline_path(project, args.id, '/cancel'))
    print(f"pipeline {args.id}: cancel requested")


def cmd_job_log(args):
    print_job_trace(resolve_project(args), args.id)


def cmd_job_retry(args):
    project = resolve_project(args)
    api('POST', job_path(project, args.id, '/retry'))
    print(f"job {args.id}: retry requested")


def cmd_job_cancel(args):
    project = resolve_project(args)
    api('POST', job_path(project, args.id, '/cancel'))
    print(f"job {args.id}: cancel requested")


# --- project settings ---------------------------------------------------------

def cmd_project_view(args):
    project = resolve_project(args)
    info = api('GET', project_path(project))
    print(f"{info['path_with_namespace']}: {info['visibility']}" + (', archived' if info.get('archived') else ''))
    print(f"default branch: {info['default_branch']}")
    print(f"merge method: {info.get('merge_method', '-')}  squash: {info.get('squash_option', '-')}  "
          f"remove source branch after merge: {info.get('remove_source_branch_after_merge')}")
    print(f"pipelines must succeed: {info.get('only_allow_merge_if_pipeline_succeeds')}  "
          f"threads must be resolved: {info.get('only_allow_merge_if_all_discussions_are_resolved')}")
    print()
    print('protected branches:')
    protected = api('GET', project_path(project, '/protected_branches'))
    if not protected:
        print('  none')
    for branch in protected:
        push = ', '.join(level['access_level_description'] for level in branch.get('push_access_levels', [])) or 'no one'
        merge = ', '.join(level['access_level_description'] for level in branch.get('merge_access_levels', [])) or 'no one'
        print(f"  {branch['name']}: push by {push}; merge by {merge}; force push: {branch.get('allow_force_push')}")


# --- project settings: changes (admin) ----------------------------------------

ACCESS_LEVELS = {'none': 0, 'developer': 30, 'maintainer': 40, 'admin': 60}


def parse_settings(pairs):
    """--set name=value pairs; a value that is JSON (true, 3, ["a"]) is taken as such, anything else as a string."""
    body = {}
    for pair in pairs:
        name, sep, value = pair.partition('=')
        if not sep or not name:
            raise SystemExit(f"isotty-gitlab: --set expects name=value, got {pair!r}")
        try:
            body[name] = json.loads(value)
        except ValueError:
            body[name] = value
    return body


def cmd_project_edit(args):
    project = resolve_project(args)
    info = api('PUT', project_path(project), body=parse_settings(args.set))
    print(f"{info['path_with_namespace']} updated")


def cmd_project_protect(args):
    project = resolve_project(args)
    body = {'name': args.branch, 'push_access_level': ACCESS_LEVELS[args.push], 'merge_access_level': ACCESS_LEVELS[args.merge],
            'allow_force_push': args.allow_force_push == 'on'}
    if args.unprotect:
        body['unprotect_access_level'] = ACCESS_LEVELS[args.unprotect]
    if args.code_owner_approval:
        body['code_owner_approval_required'] = args.code_owner_approval == 'on'
    protected = api('POST', project_path(project, '/protected_branches'), body=body)
    push = ', '.join(level['access_level_description'] for level in protected.get('push_access_levels', [])) or 'no one'
    merge = ', '.join(level['access_level_description'] for level in protected.get('merge_access_levels', [])) or 'no one'
    print(f"{protected['name']}: push by {push}; merge by {merge}; force push: {protected.get('allow_force_push')}")


def cmd_project_unprotect(args):
    project = resolve_project(args)
    api('DELETE', project_path(project, '/protected_branches/' + urllib.parse.quote(args.branch, safe='')))
    print(f"{args.branch}: protection removed")


# --- argument parsing ---------------------------------------------------------

def main():
    # git's own options must reach git untouched, so fetch/push bypass argparse.
    if len(sys.argv) > 1 and sys.argv[1] in ('fetch', 'push'):
        cmd_git(sys.argv[1], sys.argv[2:])
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command', required=True)

    p = sub.add_parser('status', help='check that the relay is reachable and list the pinned projects')
    p.set_defaults(func=cmd_status)

    for name in ('fetch', 'push'):
        sub.add_parser(name, help=f'run `git {name}` through the relay; every following argument goes to git')

    project_parent = argparse.ArgumentParser(add_help=False)
    project_parent.add_argument('--project', help='host/group/project; defaults to the project behind origin')

    mr = sub.add_parser('mr', help='merge requests').add_subparsers(dest='mr_command', required=True)
    p = mr.add_parser('list', parents=[project_parent])
    p.add_argument('--state', choices=['opened', 'closed', 'merged', 'all'], default='opened')
    p.set_defaults(func=cmd_mr_list)
    p = mr.add_parser('view', parents=[project_parent])
    p.add_argument('iid', type=int)
    p.add_argument('--diff', action='store_true', help='print the diff instead of the description')
    p.add_argument('--comments', action='store_true', help='include the notes')
    p.set_defaults(func=cmd_mr_view)
    p = mr.add_parser('pipelines', parents=[project_parent], help='list the pipelines of a merge request')
    p.add_argument('iid', type=int)
    p.set_defaults(func=cmd_mr_pipelines)
    p = mr.add_parser('create', parents=[project_parent], help='create a merge request; the description is read from stdin')
    p.add_argument('--title', required=True)
    p.add_argument('--target', required=True, help='target branch')
    p.add_argument('--source', help='source branch; defaults to the current branch')
    p.set_defaults(func=cmd_mr_create)
    p = mr.add_parser('comment', parents=[project_parent], help='comment on a merge request; the text is read from stdin')
    p.add_argument('iid', type=int)
    p.set_defaults(func=cmd_mr_comment)
    p = mr.add_parser('edit', parents=[project_parent])
    p.add_argument('iid', type=int)
    p.add_argument('--title')
    p.add_argument('--body-file')
    p.set_defaults(func=cmd_mr_edit)

    issue = sub.add_parser('issue', help='issues').add_subparsers(dest='issue_command', required=True)
    p = issue.add_parser('list', parents=[project_parent])
    p.add_argument('--state', choices=['opened', 'closed', 'all'], default='opened')
    p.add_argument('--label', action='append')
    p.set_defaults(func=cmd_issue_list)
    p = issue.add_parser('view', parents=[project_parent])
    p.add_argument('iid', type=int)
    p.add_argument('--comments', action='store_true')
    p.set_defaults(func=cmd_issue_view)
    p = issue.add_parser('create', parents=[project_parent], help='create an issue; the description is read from stdin')
    p.add_argument('--title', required=True)
    p.add_argument('--label', action='append')
    p.set_defaults(func=cmd_issue_create)
    p = issue.add_parser('comment', parents=[project_parent], help='comment on an issue; the text is read from stdin')
    p.add_argument('iid', type=int)
    p.set_defaults(func=cmd_issue_comment)
    p = issue.add_parser('edit', parents=[project_parent])
    p.add_argument('iid', type=int)
    p.add_argument('--title')
    p.add_argument('--body-file')
    p.add_argument('--label', action='append', help='replaces the label set')
    p.set_defaults(func=cmd_issue_edit)

    pipeline = sub.add_parser('pipeline', help='pipelines').add_subparsers(dest='pipeline_command', required=True)
    p = pipeline.add_parser('list', parents=[project_parent])
    p.add_argument('--ref', help='branch or tag')
    p.set_defaults(func=cmd_pipeline_list)
    p = pipeline.add_parser('view', parents=[project_parent])
    p.add_argument('id', type=int)
    p.set_defaults(func=cmd_pipeline_view)
    p = pipeline.add_parser('log', parents=[project_parent], help='print the job logs of a pipeline')
    p.add_argument('id', type=int)
    p.set_defaults(func=cmd_pipeline_log)
    p = pipeline.add_parser('retry', parents=[project_parent], help='retry the failed jobs of a pipeline')
    p.add_argument('id', type=int)
    p.set_defaults(func=cmd_pipeline_retry)
    p = pipeline.add_parser('cancel', parents=[project_parent])
    p.add_argument('id', type=int)
    p.set_defaults(func=cmd_pipeline_cancel)

    job = sub.add_parser('job', help='jobs').add_subparsers(dest='job_command', required=True)
    p = job.add_parser('log', parents=[project_parent])
    p.add_argument('id', type=int)
    p.set_defaults(func=cmd_job_log)
    p = job.add_parser('retry', parents=[project_parent])
    p.add_argument('id', type=int)
    p.set_defaults(func=cmd_job_retry)
    p = job.add_parser('cancel', parents=[project_parent])
    p.add_argument('id', type=int)
    p.set_defaults(func=cmd_job_cancel)

    project = sub.add_parser('project', help='project settings; changes need the admin level').add_subparsers(dest='project_command', required=True)
    p = project.add_parser('view', parents=[project_parent], help='show the settings an agent needs to work within')
    p.set_defaults(func=cmd_project_view)
    p = project.add_parser('edit', parents=[project_parent], help='change project settings, e.g. --set only_allow_merge_if_pipeline_succeeds=true --set ci_config_path=ci/.gitlab-ci.yml')
    p.add_argument('--set', action='append', required=True, metavar='FIELD=VALUE',
                   help='a general or CI/CD setting of the project as the API names it (description, topics, default_branch, '
                        'merge_method, squash_option, remove_source_branch_after_merge, only_allow_merge_if_pipeline_succeeds, '
                        '*_access_level, ci_config_path, build_timeout, shared_runners_enabled, ...); a refusal lists the '
                        'accepted fields; repeatable')
    p.set_defaults(func=cmd_project_edit)
    p = project.add_parser('protect', parents=[project_parent], help='protect a branch or a wildcard such as release/*; an already protected branch must be unprotected first')
    p.add_argument('branch')
    p.add_argument('--push', choices=sorted(ACCESS_LEVELS), default='maintainer', help='who may push')
    p.add_argument('--merge', choices=sorted(ACCESS_LEVELS), default='maintainer', help='who may merge')
    p.add_argument('--unprotect', choices=sorted(ACCESS_LEVELS), help='who may unprotect')
    p.add_argument('--allow-force-push', choices=['on', 'off'], default='off')
    p.add_argument('--code-owner-approval', choices=['on', 'off'])
    p.set_defaults(func=cmd_project_protect)
    p = project.add_parser('unprotect', parents=[project_parent])
    p.add_argument('branch')
    p.set_defaults(func=cmd_project_unprotect)

    args = parser.parse_args()
    args.func(args)


if __name__ == '__main__':
    main()
