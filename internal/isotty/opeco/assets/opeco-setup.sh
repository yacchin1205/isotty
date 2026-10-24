set -euo pipefail

version="__OPECO_VERSION__"
qr_port="__OPECO_QR_PORT__"
install_claude="__INSTALL_CLAUDE__"
install_codex="__INSTALL_CODEX__"
releases="https://github.com/yacchin1205/opeco-link/releases/download/v${version}"
raw="https://raw.githubusercontent.com/yacchin1205/opeco-link/v${version}"
marker=/usr/local/share/isotty/opeco-version
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if [ ! -f "$marker" ] || [ "$(cat "$marker")" != "$version" ]; then
  arch="$(dpkg --print-architecture)"
  archive="opeco_${version}_linux_${arch}.tar.gz"
  curl -fsSL -o "$tmp/$archive" "$releases/$archive"
  curl -fsSL -o "$tmp/checksums.txt" "$releases/checksums.txt"
  (cd "$tmp" && sha256sum --ignore-missing -c checksums.txt)
  tar -xzf "$tmp/$archive" -C "$tmp" opeco
  sudo install -m 0755 "$tmp/opeco" /usr/local/bin/opeco
  sudo mkdir -p "$(dirname "$marker")"
  echo "$version" | sudo tee "$marker" >/dev/null
fi

printf %s "__MERGE_SCRIPT_B64__" | base64 -d > "$tmp/merge.py"

if [ "$install_claude" = 1 ]; then
  mkdir -p "$HOME/.claude/skills/opeco-link/references"
  curl -fsSL -o "$HOME/.claude/skills/opeco-link/SKILL.md" "$raw/skills/opeco-link/SKILL.md"
  curl -fsSL -o "$HOME/.claude/skills/opeco-link/references/security.md" "$raw/skills/opeco-link/references/security.md"
  curl -fsSL -o "$tmp/claude-hooks.json" "$raw/examples/agent-hooks/claude.json"
  python3 "$tmp/merge.py" mcp-json "$HOME/.claude.json" opeco-link /usr/local/bin/opeco --no-browser --qr-port "$qr_port" mcp
  python3 "$tmp/merge.py" hooks "$HOME/.claude/settings.json" "$tmp/claude-hooks.json" opeco-link
fi

if [ "$install_codex" = 1 ]; then
  curl -fsSL -o "$tmp/codex-hooks.json" "$raw/examples/agent-hooks/codex.json"
  python3 "$tmp/merge.py" mcp-toml "$HOME/.codex/config.toml" opeco-link /usr/local/bin/opeco --no-browser --qr-port "$qr_port" mcp
  python3 "$tmp/merge.py" hooks "$HOME/.codex/hooks.json" "$tmp/codex-hooks.json" opeco-link
fi
