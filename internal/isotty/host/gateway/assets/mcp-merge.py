#!/usr/bin/env python3
"""Merge an MCP server, and its agent hooks, into agent config files.

mcp-json PATH NAME ENV_JSON COMMAND ARG...        ~/.claude.json style {"mcpServers": {...}}, stdio
mcp-json-url PATH NAME URL                        the same for a streamable HTTP server
mcp-toml PATH LABEL NAME ENV_JSON COMMAND ARG...  ~/.codex/config.toml, kept between "# isotty LABEL" markers
mcp-toml-url PATH LABEL NAME URL [LINE...]        the same for a streamable HTTP server, LINEs
                                                  appended to its table as written
hooks PATH HOOKS_JSON SERVER                      merge the "hooks" object of HOOKS_JSON, replacing
                                                  groups that already call SERVER
"""
import json
import os
import sys
import tomllib


def load_json(path):
    if not os.path.exists(path):
        return {}
    with open(path, encoding='utf-8') as f:
        return json.load(f)


def save_json(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, 'w', encoding='utf-8') as f:
        json.dump(data, f, indent=2, ensure_ascii=False)
        f.write('\n')


def mcp_json(path, name, env_json, command, *args):
    server = {'type': 'stdio', 'command': command, 'args': list(args)}
    if env := json.loads(env_json):
        server['env'] = env
    save_mcp_json(path, name, server)


def mcp_json_url(path, name, url):
    save_mcp_json(path, name, {'type': 'http', 'url': url})


def save_mcp_json(path, name, server):
    data = load_json(path)
    data.setdefault('mcpServers', {})[name] = server
    save_json(path, data)


def mcp_toml(path, label, name, env_json, command, *args):
    table = f'command = {json.dumps(command)}\nargs = {json.dumps(list(args))}\n'
    if env := json.loads(env_json):
        table += 'env = { ' + ', '.join(f'{json.dumps(k)} = {json.dumps(v)}' for k, v in env.items()) + ' }\n'
    save_mcp_toml(path, label, name, table)


def mcp_toml_url(path, label, name, url, *lines):
    save_mcp_toml(path, label, name, f'url = {json.dumps(url)}\n' + ''.join(line + '\n' for line in lines))


def save_mcp_toml(path, label, name, table):
    begin = f'# isotty {label} begin'
    end = f'# isotty {label} end'
    text = ''
    if os.path.exists(path):
        with open(path, encoding='utf-8') as f:
            text = f.read()
    if begin in text:
        head, rest = text.split(begin, 1)
        _, tail = rest.split(end, 1)
        text = head.rstrip('\n') + '\n' + tail.lstrip('\n')
    block = f'{begin}\n[mcp_servers.{name}]\n{table}{end}\n'
    text = (text.rstrip('\n') + '\n\n' if text.strip() else '') + block
    # A table the person wrote by hand for the same server makes this a duplicate; refuse to write.
    tomllib.loads(text)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, 'w', encoding='utf-8') as f:
        f.write(text)


def hooks(path, hooks_path, server):
    data = load_json(path)
    with open(hooks_path, encoding='utf-8') as f:
        source = json.load(f)['hooks']
    target = data.setdefault('hooks', {})
    for event, groups in source.items():
        kept = [g for g in target.get(event, []) if not any(h.get('server') == server for h in g['hooks'])]
        target[event] = kept + groups
    save_json(path, data)


{
    'mcp-json': mcp_json, 'mcp-json-url': mcp_json_url,
    'mcp-toml': mcp_toml, 'mcp-toml-url': mcp_toml_url,
    'hooks': hooks,
}[sys.argv[1]](*sys.argv[2:])
