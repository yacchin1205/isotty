#!/usr/bin/env python3
"""Merge an MCP server, its agent hooks, or a default setting into agent config files, or take
the server and its hooks out again.

mcp-json PATH NAME ENV_JSON COMMAND ARG...        ~/.claude.json style {"mcpServers": {...}}, stdio
mcp-json-url PATH NAME URL                        the same for a streamable HTTP server
mcp-json-remove PATH NAME                         take the server out of such a file
mcp-toml PATH NAME ENV_JSON COMMAND ARG...        ~/.codex/config.toml, as its [mcp_servers.NAME] table
mcp-toml-url PATH NAME URL [EXTRA_JSON]           the same for a streamable HTTP server, EXTRA_JSON's
                                                  keys added to its table
mcp-toml-remove PATH NAME                         take the table out of such a file
hooks PATH HOOKS_JSON SERVER                      merge the "hooks" object of HOOKS_JSON, replacing
                                                  groups that already call SERVER
hooks-remove PATH SERVER                          take out the groups that call SERVER
json-default PATH KEY VALUE_JSON                  set a top-level key of a JSON file unless it is set
toml-default PATH TABLE KEY VALUE_JSON            set a key of a [TABLE] in a TOML file unless it is set

A remove leaves a file that has nothing to take out as it is, a missing file missing.
"""
import json
import os
import sys
import tomli_w
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


def mcp_json_remove(path, name):
    data = load_json(path)
    if name in data.get('mcpServers', {}):
        del data['mcpServers'][name]
        save_json(path, data)


def mcp_toml(path, name, env_json, command, *args):
    server = {'command': command, 'args': list(args)}
    if env := json.loads(env_json):
        server['env'] = env
    save_mcp_toml(path, name, server)


def mcp_toml_url(path, name, url, extra_json='{}'):
    save_mcp_toml(path, name, {'url': url, **json.loads(extra_json)})


def save_mcp_toml(path, name, server):
    data = load_toml(path)
    data.setdefault('mcp_servers', {})[name] = server
    save_toml(path, data)


def mcp_toml_remove(path, name):
    data = load_toml(path)
    if name in data.get('mcp_servers', {}):
        del data['mcp_servers'][name]
        save_toml(path, data)


def hooks(path, hooks_path, server):
    data = load_json(path)
    with open(hooks_path, encoding='utf-8') as f:
        source = json.load(f)['hooks']
    target = data.setdefault('hooks', {})
    for event, groups in source.items():
        target[event] = without(target.get(event, []), server) + groups
    save_json(path, data)


def hooks_remove(path, server):
    data = load_json(path)
    target = data.get('hooks', {})
    kept = {event: without(groups, server) for event, groups in target.items()}
    if kept == target:
        return
    data['hooks'] = {event: groups for event, groups in kept.items() if groups}
    save_json(path, data)


def without(groups, server):
    return [g for g in groups if not any(h.get('server') == server for h in g['hooks'])]


def json_default(path, key, value_json):
    data = load_json(path)
    if key in data:
        return
    data[key] = json.loads(value_json)
    save_json(path, data)


def toml_default(path, table, key, value_json):
    data = load_toml(path)
    if key in data.get(table, {}):
        return
    data.setdefault(table, {})[key] = json.loads(value_json)
    save_toml(path, data)


def load_toml(path):
    if not os.path.exists(path):
        return {}
    with open(path, 'rb') as f:
        return tomllib.load(f)


def save_toml(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, 'wb') as f:
        tomli_w.dump(data, f)


{
    'mcp-json': mcp_json, 'mcp-json-url': mcp_json_url, 'mcp-json-remove': mcp_json_remove,
    'mcp-toml': mcp_toml, 'mcp-toml-url': mcp_toml_url, 'mcp-toml-remove': mcp_toml_remove,
    'hooks': hooks, 'hooks-remove': hooks_remove,
    'json-default': json_default, 'toml-default': toml_default,
}[sys.argv[1]](*sys.argv[2:])
