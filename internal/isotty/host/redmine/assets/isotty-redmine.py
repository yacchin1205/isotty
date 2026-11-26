#!/usr/bin/env python3
"""Read the project's Redmine through the IsoTTY relay.

The relay runs in the host's isotty daemon and is reachable only while
the host is awake and online. It reads issues, wiki pages and attachments of
the projects pinned in .isotty/redmine.yaml with the host's key; this VM
holds none. What comes back is filtered on the host: the people Redmine
names are pseudonyms (Person- and six hex digits, the same for the same
user), and names, e-mail addresses, phone numbers and secrets in the text
are [name], [email], [phone] and [secret]. The filter misses some.
"""
import argparse
import json
import sys
import urllib.error
import urllib.parse
import urllib.request

RELAY = 'http://127.0.0.1:__ISOTTY_GATEWAY_PORT__/redmine'

UNREACHABLE = (
    'isotty-redmine: the Redmine relay at {relay} is not reachable ({reason}). '
    'The host is asleep or offline, or its isotty daemon is not running; nothing is queued. '
    'Retry once the host is back, and tell the human.'
)


def api(path, params=None, raw=False):
    url = RELAY + path
    if params:
        url += '?' + urllib.parse.urlencode({k: v for k, v in params.items() if v is not None})
    try:
        with urllib.request.urlopen(urllib.request.Request(url), timeout=600) as res:
            content = res.read()
            content_type = res.headers.get_content_type()
    except urllib.error.HTTPError as e:
        raise SystemExit('isotty-redmine: ' + describe_http_error(e))
    except urllib.error.URLError as e:
        raise SystemExit(UNREACHABLE.format(relay=RELAY, reason=e.reason))
    if raw:
        return content, content_type
    return json.loads(content)


def describe_http_error(e):
    content = e.read()
    if e.headers.get_content_type() == 'application/json':
        error = json.loads(content).get('error')
        if isinstance(error, str):
            return error
    return f"HTTP {e.code}: {content.decode(errors='replace').strip()}"


def resolve_project(args):
    if args.project:
        return args.project
    projects = api('/status')['projects']
    if len(projects) == 1:
        return projects[0]['identifier']
    names = ', '.join(p['identifier'] for p in projects)
    raise SystemExit(f"isotty-redmine: --project is needed; the pinned projects are: {names}")


def name_of(item):
    return (item or {}).get('name', '-')


def cmd_status(args):
    status = api('/status')
    print(f"redmine: {status['url']}")
    print('projects:')
    for project in status['projects']:
        print(f"  {project['identifier']}\t{project['name']}")


def list_issues(project, status, limit):
    status_id = {'open': 'open', 'closed': 'closed', 'all': '*'}[status]
    issues = []
    offset = 0
    while True:
        page = min(100, limit - len(issues)) if limit else 100
        answer = api(f"/projects/{urllib.parse.quote(project, safe='')}/issues",
                     {'status_id': status_id, 'limit': page, 'offset': offset, 'sort': 'id'})
        issues.extend(answer.get('issues', []))
        offset += len(answer.get('issues', []))
        if not answer.get('issues') or offset >= answer.get('total_count', 0) or (limit and len(issues) >= limit):
            return issues


def cmd_issue_list(args):
    issues = list_issues(resolve_project(args), args.status, args.limit)
    if args.json:
        json.dump(issues, sys.stdout, ensure_ascii=False, indent=1)
        print()
        return
    print('| # | tracker | status | priority | subject | assigned |')
    print('|---|---|---|---|---|---|')
    for issue in issues:
        subject = issue.get('subject', '').replace('|', '\\|')
        print(f"| {issue['id']} | {name_of(issue.get('tracker'))} | {name_of(issue.get('status'))} | "
              f"{name_of(issue.get('priority'))} | {subject} | {name_of(issue.get('assigned_to'))} |")


def fetch_issue(number):
    return api(f"/issues/{int(number)}")['issue']


def print_issue(issue):
    print(f"# #{issue['id']} {issue.get('subject', '')}")
    print()
    for label, value in [('project', name_of(issue.get('project'))), ('tracker', name_of(issue.get('tracker'))),
                         ('status', name_of(issue.get('status'))), ('priority', name_of(issue.get('priority'))),
                         ('author', name_of(issue.get('author'))), ('assigned', name_of(issue.get('assigned_to'))),
                         ('created', issue.get('created_on', '-')), ('updated', issue.get('updated_on', '-'))]:
        print(f"- {label}: {value}")
    if issue.get('parent'):
        print(f"- parent: #{issue['parent']['id']}")
    if issue.get('description'):
        print()
        print('## Description')
        print()
        print(issue['description'])
    children = issue.get('children') or []
    if children:
        print()
        print('## Children')
        print()
        for child in children:
            print(f"- #{child['id']} {child.get('subject', '')}")
    relations = issue.get('relations') or []
    if relations:
        print()
        print('## Relations')
        print()
        for rel in relations:
            other = rel['issue_to_id'] if rel['issue_id'] == issue['id'] else rel['issue_id']
            print(f"- {rel['relation_type']} #{other}")
    notes = [j for j in issue.get('journals') or [] if j.get('notes')]
    if notes:
        print()
        print('## Notes')
        for journal in notes:
            print()
            print(f"### {name_of(journal.get('user'))} on {journal.get('created_on', '-')}")
            print()
            print(journal['notes'])
    attachments = issue.get('attachments') or []
    if attachments:
        print()
        print('## Attachments')
        print()
        for a in attachments:
            print(f"- {a['id']}: {a['filename']} ({a.get('content_type', '?')}, {a.get('filesize', 0)} bytes)")


def cmd_issue_view(args):
    issue = fetch_issue(args.id)
    if args.json:
        json.dump(issue, sys.stdout, ensure_ascii=False, indent=1)
        print()
        return
    print_issue(issue)


def cmd_issue_children(args):
    for child in fetch_issue(args.id).get('children') or []:
        print(f"#{child['id']}\t{name_of(child.get('tracker'))}\t{child.get('subject', '')}")


def cmd_issue_related(args):
    """Walk relations, children and the parent up to --hops away. An issue
    the relay refuses (another project) is reported and not followed."""
    queue = [(int(args.id), 0)]
    seen = set()
    while queue:
        number, hops = queue.pop(0)
        if number in seen:
            continue
        seen.add(number)
        try:
            issue = api(f"/issues/{number}")['issue']
        except SystemExit as e:
            print(f"#{number}\t(not readable: {e})")
            continue
        print(f"#{issue['id']}\t{name_of(issue.get('status'))}\t{issue.get('subject', '')}")
        if hops >= args.hops:
            continue
        for child in issue.get('children') or []:
            queue.append((child['id'], hops + 1))
        for rel in issue.get('relations') or []:
            other = rel['issue_to_id'] if rel['issue_id'] == issue['id'] else rel['issue_id']
            queue.append((other, hops + 1))
        if issue.get('parent'):
            queue.append((issue['parent']['id'], hops + 1))


def cmd_wiki_list(args):
    project = resolve_project(args)
    for page in api(f"/projects/{urllib.parse.quote(project, safe='')}/wiki").get('wiki_pages', []):
        print(f"{page['title']}\tv{page.get('version', '?')}\t{page.get('updated_on', '-')}")


def fetch_wiki(project, title):
    return api(f"/projects/{urllib.parse.quote(project, safe='')}/wiki/{urllib.parse.quote(title, safe='')}")['wiki_page']


def cmd_wiki_view(args):
    page = fetch_wiki(resolve_project(args), args.title)
    if args.json:
        json.dump(page, sys.stdout, ensure_ascii=False, indent=1)
        print()
        return
    print(f"# {page['title']} (v{page.get('version', '?')}, {name_of(page.get('author'))}, {page.get('updated_on', '-')})")
    print()
    print(page.get('text', ''))
    attachments = page.get('attachments') or []
    if attachments:
        print()
        print('## Attachments')
        print()
        for a in attachments:
            print(f"- {a['id']}: {a['filename']} ({a.get('content_type', '?')}, {a.get('filesize', 0)} bytes)")


def cmd_attachment_get(args):
    if bool(args.issue) == bool(args.wiki):
        raise SystemExit('isotty-redmine: name where the attachment is listed with --issue <id> or --wiki <project>/<title>')
    if args.issue:
        listed = fetch_issue(args.issue).get('attachments') or []
        path = f"/issues/{int(args.issue)}/attachments/{int(args.id)}/download"
    else:
        project, _, title = args.wiki.partition('/')
        listed = fetch_wiki(project, title).get('attachments') or []
        path = f"/projects/{urllib.parse.quote(project, safe='')}/wiki/{urllib.parse.quote(title, safe='')}/attachments/{int(args.id)}/download"
    found = next((a for a in listed if a['id'] == int(args.id)), None)
    if found is None:
        raise SystemExit(f"isotty-redmine: attachment {args.id} is not listed there")
    content, _ = api(path, raw=True)
    extracted = not found.get('content_type', '').startswith('text/') and found.get('content_type') != 'application/json'
    out = args.out or (found['filename'] + '.txt' if extracted else found['filename'])
    with open(out, 'wb') as f:
        f.write(content)
    print(f"{out}: {len(content)} bytes" + (' (the text read on the host)' if extracted else ''))


def main():
    parser = argparse.ArgumentParser(prog='isotty-redmine', description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command', required=True)
    sub.add_parser('status', help='the Redmine and the pinned projects').set_defaults(func=cmd_status)

    issue = sub.add_parser('issue', help='issues').add_subparsers(dest='issue_command', required=True)
    p = issue.add_parser('list', help='issues of a project')
    p.add_argument('--project')
    p.add_argument('--status', choices=['open', 'closed', 'all'], default='open')
    p.add_argument('--limit', type=int, default=0, help='at most this many (0 = all)')
    p.add_argument('--json', action='store_true')
    p.set_defaults(func=cmd_issue_list)
    p = issue.add_parser('view', help='one issue with its notes and attachments')
    p.add_argument('id')
    p.add_argument('--json', action='store_true')
    p.set_defaults(func=cmd_issue_view)
    p = issue.add_parser('children', help='the direct children of an issue')
    p.add_argument('id')
    p.set_defaults(func=cmd_issue_children)
    p = issue.add_parser('related', help='issues linked by relations, children and parent')
    p.add_argument('id')
    p.add_argument('--hops', type=int, default=1)
    p.set_defaults(func=cmd_issue_related)

    wiki = sub.add_parser('wiki', help='wiki pages').add_subparsers(dest='wiki_command', required=True)
    p = wiki.add_parser('list', help='pages of a project')
    p.add_argument('--project')
    p.set_defaults(func=cmd_wiki_list)
    p = wiki.add_parser('view', help='one page')
    p.add_argument('title')
    p.add_argument('--project')
    p.add_argument('--json', action='store_true')
    p.set_defaults(func=cmd_wiki_view)

    attachment = sub.add_parser('attachment', help='attachments').add_subparsers(dest='attachment_command', required=True)
    p = attachment.add_parser('get', help='download an attachment listed on an issue or a wiki page')
    p.add_argument('id')
    p.add_argument('--issue', help='the issue that lists it')
    p.add_argument('--wiki', help='the wiki page that lists it, as <project>/<title>')
    p.add_argument('--out', help='where to write it (default: its file name in the current directory, with .txt added for a PDF or an image)')
    p.set_defaults(func=cmd_attachment_get)

    args = parser.parse_args()
    args.func(args)


if __name__ == '__main__':
    main()
