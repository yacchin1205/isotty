#!/usr/bin/env python3
"""Merge the opeco MCP server and its agent hooks into agent config files.

mcp-json PATH NAME ENV_JSON COMMAND ARG...   ~/.claude.json style {"mcpServers": {...}}
mcp-toml PATH NAME ENV_JSON COMMAND ARG...   ~/.codex/config.toml, kept between marker comments
hooks PATH HOOKS_JSON SERVER                 merge the "hooks" object of HOOKS_JSON, replacing
                                             groups that already call SERVER
"""
import json
import os
import sys
import tomllib

TOML_BEGIN = '# isotty opeco begin'
TOML_END = '# isotty opeco end'


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
    data = load_json(path)
    server = {'type': 'stdio', 'command': command, 'args': list(args)}
    if env := json.loads(env_json):
        server['env'] = env
    data.setdefault('mcpServers', {})[name] = server
    save_json(path, data)


def mcp_toml(path, name, env_json, command, *args):
    text = ''
    if os.path.exists(path):
        with open(path, encoding='utf-8') as f:
            text = f.read()
    if TOML_BEGIN in text:
        head, rest = text.split(TOML_BEGIN, 1)
        _, tail = rest.split(TOML_END, 1)
        text = head.rstrip('\n') + '\n' + tail.lstrip('\n')
    block = f'{TOML_BEGIN}\n[mcp_servers.{name}]\ncommand = {json.dumps(command)}\nargs = {json.dumps(list(args))}\n'
    if env := json.loads(env_json):
        block += 'env = { ' + ', '.join(f'{json.dumps(k)} = {json.dumps(v)}' for k, v in env.items()) + ' }\n'
    block += f'{TOML_END}\n'
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


{'mcp-json': mcp_json, 'mcp-toml': mcp_toml, 'hooks': hooks}[sys.argv[1]](*sys.argv[2:])
