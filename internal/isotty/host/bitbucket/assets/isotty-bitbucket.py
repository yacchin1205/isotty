#!/usr/bin/env python3
"""Work with the project's Bitbucket repositories through the IsoTTY relay.

The relay runs on the host inside `isotty attach` and is reachable only
while the host is attached. It forwards git fetch/push and a fixed set of
pull request, pipeline and repository-settings operations for the
repositories pinned in .isotty/bitbucket.yaml, with the host's credential;
this VM holds none. Reads, comments, pushes and settings changes each need
their own pin level: read, comment, write, admin.
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

RELAY = 'http://127.0.0.1:__ISOTTY_GATEWAY_PORT__/bitbucket'
API = RELAY + '/api'
GIT_BASE = RELAY + '/git/'

UNREACHABLE = (
    'isotty-bitbucket: the Bitbucket relay at {relay} is not reachable ({reason}). '
    'It is available only while the host is attached with `isotty attach`; nothing is queued. '
    'Keep your commits and text under /workspace and retry after re-attach.'
)

REMOTE_PATTERNS = [
    re.compile(r'^https://(?:[^@/]+@)?bitbucket\.org/([^/]+)/([^/]+?)(?:\.git)?/?$'),
    re.compile(r'^ssh://git@(?:altssh\.)?bitbucket\.org(?::[0-9]+)?/([^/]+)/([^/]+?)(?:\.git)?/?$'),
    re.compile(r'^git@bitbucket\.org:([^/]+)/([^/]+?)(?:\.git)?/?$'),
]

UUID = re.compile(r'^\{?([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\}?$')


def api(method, path, params=None, body=None, raw=False):
    url = API + path
    if params:
        url += ('&' if '?' in url else '?') + urllib.parse.urlencode({k: v for k, v in params.items() if v is not None}, doseq=True)
    data = None
    headers = {}
    if raw:
        # A diff or a log is served as text or octet-stream, and Bitbucket
        # answers 406 when only JSON is accepted, which the relay asks for
        # unless the request says otherwise.
        headers['accept'] = '*/*'
    if body is not None:
        data = json.dumps(body).encode()
        headers['content-type'] = 'application/json'
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=300) as res:
            content = res.read()
    except urllib.error.HTTPError as e:
        raise SystemExit('isotty-bitbucket: ' + describe_http_error(e))
    except urllib.error.URLError as e:
        raise SystemExit(UNREACHABLE.format(relay=RELAY, reason=e.reason))
    if raw:
        return content
    return json.loads(content) if content else None


def paged(path, params=None):
    """Every value of a paginated answer. Bitbucket points at the next page
    with an absolute URL of api.bitbucket.org; the path is taken from it and
    asked of the relay again."""
    while True:
        page = api('GET', path, params)
        yield from page.get('values', [])
        following = page.get('next')
        if not following:
            return
        parsed = urllib.parse.urlsplit(following)
        path = parsed.path[len('/2.0'):] if parsed.path.startswith('/2.0/') else parsed.path
        if parsed.query:
            path += '?' + parsed.query
        params = None


def describe_http_error(e):
    content = e.read()
    if e.headers.get_content_type() == 'application/json':
        payload = json.loads(content)
        error = payload.get('error')
        # The relay answers {"error": "..."}; Bitbucket answers {"error": {"message": ..., "detail": ...}}.
        if isinstance(error, str):
            return error
        if isinstance(error, dict):
            detail = error.get('detail')
            return f"Bitbucket answered {e.code}: {error.get('message', '')}" + (f" ({detail})" if detail else '')
    return f"HTTP {e.code}: {content.decode(errors='replace').strip()}"


def git(*args, check=True):
    return subprocess.run(['git', *args], check=check, capture_output=True, text=True)


def parse_remote_url(url):
    for pattern in REMOTE_PATTERNS:
        m = pattern.match(url)
        if m:
            return f"{m.group(1)}/{m.group(2)}"
    raise SystemExit(f"isotty-bitbucket: {url} is not a bitbucket.org repository URL")


def resolve_repo(args):
    if args.repo:
        return args.repo
    result = git('remote', 'get-url', 'origin', check=False)
    if result.returncode != 0:
        raise SystemExit('isotty-bitbucket: no origin remote in the current directory; pass --repo workspace/repo_slug')
    return parse_remote_url(result.stdout.strip())


def current_branch():
    return git('rev-parse', '--abbrev-ref', 'HEAD').stdout.strip()


def read_body(args):
    if getattr(args, 'body_file', None):
        with open(args.body_file, encoding='utf-8') as handle:
            return handle.read()
    if sys.stdin.isatty():
        raise SystemExit('isotty-bitbucket: the body is read from stdin; redirect a file into the command')
    return sys.stdin.read()


# --- git ---------------------------------------------------------------------

def cmd_git(command, git_args):
    """Run git with every Bitbucket URL rewritten to the relay for this one
    invocation. The project's remotes are rewritten one by one, since
    Bitbucket writes the user's name into HTTPS remotes and SSH ones may go
    through altssh; the bare forms cover a URL given on the command line."""
    rewrite = []
    seen = set()
    result = git('remote', check=False)
    for remote in result.stdout.split() if result.returncode == 0 else []:
        url = git('remote', 'get-url', remote, check=False).stdout.strip()
        for pattern in REMOTE_PATTERNS:
            m = pattern.match(url)
            if m and url not in seen:
                seen.add(url)
                rewrite += ['-c', f'url.{GIT_BASE}{m.group(1)}/{m.group(2)}.git.insteadOf={url}']
    for base in ('https://bitbucket.org/', 'git@bitbucket.org:', 'ssh://git@bitbucket.org/'):
        rewrite += ['-c', f'url.{GIT_BASE}.insteadOf={base}']
    env = dict(os.environ, GIT_TERMINAL_PROMPT='0')
    result = subprocess.run(['git', *rewrite, command, *git_args], env=env)
    if result.returncode != 0:
        print('isotty-bitbucket: git failed. A "403" means the repository is not pinned for this '
              'project or not pinned as write; `isotty-bitbucket status` lists what this VM may reach. '
              'A connection error means the host is not attached.', file=sys.stderr)
    raise SystemExit(result.returncode)


# --- status -------------------------------------------------------------------

def cmd_status(args):
    url = RELAY + '/status'
    try:
        with urllib.request.urlopen(url, timeout=30) as res:
            status = json.load(res)
    except urllib.error.HTTPError as e:
        raise SystemExit('isotty-bitbucket: ' + describe_http_error(e))
    except urllib.error.URLError as e:
        raise SystemExit(UNREACHABLE.format(relay=RELAY, reason=e.reason))
    print('Bitbucket is reachable through the isotty relay. Repositories this VM may use:')
    for repo in sorted(status['repos']):
        print(f"  {repo}\t{status['repos'][repo]}")


# --- pull requests ------------------------------------------------------------

def repo_path(repo, tail=''):
    return f"/repositories/{repo}" + tail


def pr_path(repo, number=None, tail=''):
    return repo_path(repo, '/pullrequests' + (f"/{number}" if number is not None else '') + tail)


def who(item):
    user = item.get('author') or item.get('user') or {}
    return user.get('display_name') or user.get('nickname') or '?'


def print_pr(pr):
    draft = ' (draft)' if pr.get('draft') else ''
    print(f"#{pr['id']} {pr['title']}")
    print(f"state: {pr['state']}{draft}")
    print(f"author: {who(pr)}")
    source, destination = pr['source'], pr['destination']
    commit = (source.get('commit') or {}).get('hash', '')
    print(f"destination: {destination['branch']['name']} <- source: {source['branch']['name']} ({commit[:12]})")
    print(f"url: {pr['links']['html']['href']}")
    print(f"created: {pr['created_on']}  updated: {pr['updated_on']}")
    print()
    print(pr.get('description') or '(no description)')


def print_comments(repo, number):
    for comment in paged(pr_path(repo, number, '/comments'), {'pagelen': 100}):
        if comment.get('deleted'):
            continue
        place = ''
        inline = comment.get('inline')
        if inline:
            place = f" on {inline.get('path')}:{inline.get('to') or inline.get('from')}"
        print()
        print(f"--- comment by {who(comment)} at {comment['created_on']}{place}")
        print(comment['content']['raw'])


def cmd_pr_list(args):
    repo = resolve_repo(args)
    states = ['OPEN', 'MERGED', 'DECLINED', 'SUPERSEDED'] if args.state == 'all' else [args.state.upper()]
    for pr in paged(pr_path(repo), {'state': states, 'pagelen': 50}):
        draft = ' (draft)' if pr.get('draft') else ''
        print(f"#{pr['id']}\t{pr['state']}{draft}\t{pr['source']['branch']['name']}\t{pr['title']}")


def cmd_pr_view(args):
    repo = resolve_repo(args)
    if args.diff:
        sys.stdout.write(api('GET', pr_path(repo, args.number, '/diff'), raw=True).decode(errors='replace'))
        return
    print_pr(api('GET', pr_path(repo, args.number)))
    if args.comments:
        print_comments(repo, args.number)


def cmd_pr_checks(args):
    repo = resolve_repo(args)
    statuses = list(paged(pr_path(repo, args.number, '/statuses'), {'pagelen': 100}))
    if not statuses:
        print(f"no build statuses on pull request #{args.number}")
        return
    for status in statuses:
        print(f"{status.get('name') or status.get('key')}\t{status['state']}\t{status.get('url') or '-'}")


def cmd_pr_create(args):
    repo = resolve_repo(args)
    body = {'title': args.title,
            'source': {'branch': {'name': args.source or current_branch()}},
            'destination': {'branch': {'name': args.destination}},
            'description': read_body(args), 'draft': args.draft}
    pr = api('POST', pr_path(repo), body=body)
    print(f"#{pr['id']} {pr['links']['html']['href']}")


def cmd_pr_comment(args):
    repo = resolve_repo(args)
    comment = api('POST', pr_path(repo, args.number, '/comments'), body={'content': {'raw': read_body(args)}})
    print(comment['links']['html']['href'])


def cmd_pr_edit(args):
    repo = resolve_repo(args)
    body = {}
    if args.title is not None:
        body['title'] = args.title
    if args.body_file is not None:
        body['description'] = read_body(args)
    if not body:
        raise SystemExit('isotty-bitbucket: nothing to change; pass --title and/or --body-file')
    print(api('PUT', pr_path(repo, args.number), body=body)['links']['html']['href'])


# --- pipelines ----------------------------------------------------------------

def pipeline_path(repo, uuid=None, tail=''):
    return repo_path(repo, '/pipelines' + (f"/{urllib.parse.quote(uuid, safe='')}" if uuid else '') + tail)


def outcome(state):
    """A pipeline or step state: PENDING, IN_PROGRESS, or COMPLETED with its result."""
    name = state.get('name', '?')
    result = (state.get('result') or {}).get('name')
    return name + (f"/{result}" if result else '')


def target_of(pipeline):
    target = pipeline.get('target') or {}
    ref = target.get('ref_name') or (target.get('commit') or {}).get('hash', '')[:12]
    selector = target.get('selector') or {}
    if selector.get('pattern'):
        ref += f" [{selector.get('type', '')}:{selector['pattern']}]"
    return ref


def pipeline_uuid(repo, ident):
    """A pipeline is named by its UUID, braces or not, or by the build
    number the Bitbucket page shows; the latter is looked up among the
    recent pipelines."""
    m = UUID.match(ident)
    if m:
        return '{' + m.group(1).lower() + '}'
    if not ident.isdigit():
        raise SystemExit(f"isotty-bitbucket: {ident} is neither a pipeline UUID nor a build number")
    seen = 0
    for pipeline in paged(pipeline_path(repo), {'sort': '-created_on', 'pagelen': 100}):
        if pipeline['build_number'] == int(ident):
            return pipeline['uuid']
        seen += 1
        if seen >= 1000:
            break
    raise SystemExit(f"isotty-bitbucket: no build #{ident} among the last {seen} pipelines of {repo}; give its UUID")


def cmd_pipeline_list(args):
    repo = resolve_repo(args)
    params = {'sort': '-created_on', 'pagelen': 50}
    if args.branch:
        params['target.branch'] = args.branch
    shown = 0
    for pipeline in paged(pipeline_path(repo), params):
        print(f"#{pipeline['build_number']}\t{outcome(pipeline['state'])}\t{target_of(pipeline)}\t{pipeline['uuid']}\t{pipeline['created_on']}")
        shown += 1
        if shown >= 50:
            break


def cmd_pipeline_view(args):
    repo = resolve_repo(args)
    uuid = pipeline_uuid(repo, args.id)
    pipeline = api('GET', pipeline_path(repo, uuid))
    print(f"pipeline #{pipeline['build_number']} {pipeline['uuid']}")
    print(f"state: {outcome(pipeline['state'])}")
    print(f"target: {target_of(pipeline)}  trigger: {(pipeline.get('trigger') or {}).get('name', '?')}")
    print(f"created: {pipeline['created_on']}  completed: {pipeline.get('completed_on') or '-'}  seconds: {pipeline.get('build_seconds_used', '-')}")
    print()
    for step in paged(pipeline_path(repo, uuid, '/steps'), {'pagelen': 100}):
        print(f"step {step['uuid']}\t{outcome(step.get('state') or {})}\t{step.get('name') or '(unnamed)'}")


def print_step_log(repo, uuid, step):
    sys.stdout.write(api('GET', pipeline_path(repo, uuid, f"/steps/{urllib.parse.quote(step, safe='')}/log"), raw=True).decode(errors='replace'))


def cmd_pipeline_log(args):
    repo = resolve_repo(args)
    uuid = pipeline_uuid(repo, args.id)
    if args.step:
        m = UUID.match(args.step)
        if not m:
            raise SystemExit(f"isotty-bitbucket: --step takes the step's UUID, as `pipeline view` lists it")
        print_step_log(repo, uuid, '{' + m.group(1).lower() + '}')
        return
    for step in paged(pipeline_path(repo, uuid, '/steps'), {'pagelen': 100}):
        state = step.get('state') or {}
        print(f"===== step {step['uuid']} {step.get('name') or '(unnamed)'} ({outcome(state)})")
        if state.get('name') != 'COMPLETED':
            print('(log is available once the step completes)')
            continue
        print_step_log(repo, uuid, step['uuid'])


def cmd_pipeline_stop(args):
    repo = resolve_repo(args)
    uuid = pipeline_uuid(repo, args.id)
    api('POST', pipeline_path(repo, uuid, '/stopPipeline'))
    print(f"pipeline {uuid}: stop requested")


# --- repository settings ------------------------------------------------------

def optional(path):
    """GET that reports 404 (not configured) and 403 (Bitbucket's reason, e.g. the token's scopes) instead of failing."""
    req = urllib.request.Request(API + path)
    try:
        with urllib.request.urlopen(req, timeout=60) as res:
            return json.load(res), None
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return None, 'none'
        if e.code == 403:
            return None, describe_http_error(e)
        raise SystemExit('isotty-bitbucket: ' + describe_http_error(e))
    except urllib.error.URLError as e:
        raise SystemExit(UNREACHABLE.format(relay=RELAY, reason=e.reason))


def describe_restriction(rule):
    where = rule.get('pattern') if rule.get('branch_match_kind') == 'glob' else f"{rule.get('branch_type')} branches"
    extra = ''
    if rule.get('value') is not None:
        extra += f" value={rule['value']}"
    if rule.get('kind') in ('push', 'restrict_merges'):
        people = [u.get('display_name') or u.get('nickname', '?') for u in rule.get('users') or []]
        people += [g.get('name', '?') for g in rule.get('groups') or []]
        extra += ' allowed: ' + (', '.join(people) if people else 'nobody')
    return f"{rule['id']}\t{rule['kind']}\t{where}{extra}"


def cmd_repo_view(args):
    repo = resolve_repo(args)
    info = api('GET', repo_path(repo))
    print(f"{info['full_name']}: {'private' if info.get('is_private') else 'public'}")
    print(f"main branch: {(info.get('mainbranch') or {}).get('name', '-')}")
    print(f"project: {(info.get('project') or {}).get('key', '-')}  language: {info.get('language') or '-'}  fork policy: {info.get('fork_policy', '-')}  signed commits enforced: {info.get('enforced_signed_commits')}")
    print()
    rules, why = optional(repo_path(repo, '/branch-restrictions?pagelen=100'))
    if rules is None:
        print(f"branch restrictions: {why}")
    elif not rules.get('values'):
        print('branch restrictions: none')
    else:
        print('branch restrictions:')
        for rule in rules['values']:
            print('  ' + describe_restriction(rule))


def parse_settings(pairs):
    """--set name=value pairs; a value that is JSON (true, 3, ["a"]) is taken as such, anything else as a string."""
    body = {}
    for pair in pairs:
        name, sep, value = pair.partition('=')
        if not sep or not name:
            raise SystemExit(f"isotty-bitbucket: --set expects name=value, got {pair!r}")
        try:
            body[name] = json.loads(value)
        except ValueError:
            body[name] = value
    return body


def read_json_body(path):
    with open(path, encoding='utf-8') as handle:
        return json.load(handle)


def cmd_repo_edit(args):
    repo = resolve_repo(args)
    info = api('PUT', repo_path(repo), body=parse_settings(args.set))
    print(f"{info['full_name']} updated")


def restriction_body(args):
    if args.body_file:
        return read_json_body(args.body_file)
    if not args.kind:
        raise SystemExit('isotty-bitbucket: pass --kind with --pattern or --branch-type, or --body-file <json>')
    body = {'kind': args.kind}
    if args.pattern is not None:
        body.update(branch_match_kind='glob', pattern=args.pattern)
    elif args.branch_type is not None:
        body.update(branch_match_kind='branching_model', branch_type=args.branch_type)
    else:
        raise SystemExit('isotty-bitbucket: a restriction matches branches by --pattern or by --branch-type')
    if args.value is not None:
        body['value'] = args.value
    if args.kind in ('push', 'restrict_merges'):
        body.setdefault('users', [])
        body.setdefault('groups', [])
    return body


def cmd_restriction_list(args):
    repo = resolve_repo(args)
    for rule in paged(repo_path(repo, '/branch-restrictions'), {'pagelen': 100}):
        print(describe_restriction(rule))


def cmd_restriction_view(args):
    repo = resolve_repo(args)
    print(json.dumps(api('GET', repo_path(repo, f"/branch-restrictions/{args.id}")), indent=2))


def cmd_restriction_create(args):
    repo = resolve_repo(args)
    rule = api('POST', repo_path(repo, '/branch-restrictions'), body=restriction_body(args))
    print('restriction created: ' + describe_restriction(rule))


def cmd_restriction_edit(args):
    repo = resolve_repo(args)
    rule = api('PUT', repo_path(repo, f"/branch-restrictions/{args.id}"), body=restriction_body(args))
    print('restriction updated: ' + describe_restriction(rule))


def cmd_restriction_delete(args):
    repo = resolve_repo(args)
    api('DELETE', repo_path(repo, f"/branch-restrictions/{args.id}"))
    print(f"restriction {args.id} deleted")


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
    repo_parent.add_argument('--repo', help='workspace/repo_slug; defaults to the repository behind origin')

    pr = sub.add_parser('pr', help='pull requests').add_subparsers(dest='pr_command', required=True)
    p = pr.add_parser('list', parents=[repo_parent])
    p.add_argument('--state', choices=['open', 'merged', 'declined', 'superseded', 'all'], default='open')
    p.set_defaults(func=cmd_pr_list)
    p = pr.add_parser('view', parents=[repo_parent])
    p.add_argument('number', type=int)
    p.add_argument('--diff', action='store_true', help='print the diff instead of the description')
    p.add_argument('--comments', action='store_true', help='include the comments')
    p.set_defaults(func=cmd_pr_view)
    p = pr.add_parser('checks', parents=[repo_parent], help='the build statuses on the pull request')
    p.add_argument('number', type=int)
    p.set_defaults(func=cmd_pr_checks)
    p = pr.add_parser('create', parents=[repo_parent], help='create a pull request; the description is read from stdin')
    p.add_argument('--title', required=True)
    p.add_argument('--destination', required=True, help='the branch to merge into')
    p.add_argument('--source', help='the branch to merge; defaults to the current branch')
    p.add_argument('--draft', action='store_true')
    p.set_defaults(func=cmd_pr_create)
    p = pr.add_parser('comment', parents=[repo_parent], help='comment on a pull request; the text is read from stdin')
    p.add_argument('number', type=int)
    p.set_defaults(func=cmd_pr_comment)
    p = pr.add_parser('edit', parents=[repo_parent])
    p.add_argument('number', type=int)
    p.add_argument('--title')
    p.add_argument('--body-file', help='the new description')
    p.set_defaults(func=cmd_pr_edit)

    pipeline = sub.add_parser('pipeline', help='Bitbucket Pipelines').add_subparsers(dest='pipeline_command', required=True)
    p = pipeline.add_parser('list', parents=[repo_parent], help='the most recent pipelines')
    p.add_argument('--branch')
    p.set_defaults(func=cmd_pipeline_list)
    p = pipeline.add_parser('view', parents=[repo_parent], help='a pipeline and its steps')
    p.add_argument('id', help='build number or UUID')
    p.set_defaults(func=cmd_pipeline_view)
    p = pipeline.add_parser('log', parents=[repo_parent], help='print the step logs of a pipeline')
    p.add_argument('id', help='build number or UUID')
    p.add_argument('--step', help='one step only, by its UUID')
    p.set_defaults(func=cmd_pipeline_log)
    p = pipeline.add_parser('stop', parents=[repo_parent])
    p.add_argument('id', help='build number or UUID')
    p.set_defaults(func=cmd_pipeline_stop)

    repo = sub.add_parser('repo', help='repository settings; changes need the admin level').add_subparsers(dest='repo_command', required=True)
    p = repo.add_parser('view', parents=[repo_parent], help='show the settings an agent needs to work within')
    p.set_defaults(func=cmd_repo_view)
    p = repo.add_parser('edit', parents=[repo_parent], help='change repository settings, e.g. --set fork_policy=no_public_forks --set description="..."')
    p.add_argument('--set', action='append', required=True, metavar='FIELD=VALUE',
                   help='a field of the repository (description, language, fork_policy, enforced_signed_commits); repeatable')
    p.set_defaults(func=cmd_repo_edit)

    restriction = repo.add_parser('restriction', help='branch restrictions').add_subparsers(dest='restriction_command', required=True)
    p = restriction.add_parser('list', parents=[repo_parent])
    p.set_defaults(func=cmd_restriction_list)
    p = restriction.add_parser('view', parents=[repo_parent])
    p.add_argument('id', type=int)
    p.set_defaults(func=cmd_restriction_view)
    for name, func in (('create', cmd_restriction_create), ('edit', cmd_restriction_edit)):
        p = restriction.add_parser(name, parents=[repo_parent], help=f'{name} a restriction from flags or a JSON file (kind, branch_match_kind, pattern, branch_type, value, users, groups)')
        if name == 'edit':
            p.add_argument('id', type=int)
        p.add_argument('--kind', help='push, force, delete, restrict_merges, require_approvals_to_merge, require_passing_builds_to_merge, ...')
        p.add_argument('--pattern', help='glob of the branches, e.g. main or release/*')
        p.add_argument('--branch-type', choices=['production', 'development', 'bugfix', 'release', 'feature', 'hotfix'], help='match by the branching model instead')
        p.add_argument('--value', type=int, help='the number a kind needs, e.g. approvals')
        p.add_argument('--body-file', help='send this JSON instead of the flags')
        p.set_defaults(func=func)
    p = restriction.add_parser('delete', parents=[repo_parent])
    p.add_argument('id', type=int)
    p.set_defaults(func=cmd_restriction_delete)

    args = parser.parse_args()
    args.func(args)


if __name__ == '__main__':
    main()
