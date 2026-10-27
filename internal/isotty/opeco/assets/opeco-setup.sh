set -euo pipefail

minimum_version="__OPECO_MINIMUM_VERSION__"
qr_port="__OPECO_QR_PORT__"
install_claude="__INSTALL_CLAUDE__"
install_codex="__INSTALL_CODEX__"
repository="https://github.com/yacchin1205/opeco-link"
marker=/usr/local/share/isotty/opeco-version
open_client="__OPEN_CLIENT__"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

latest="$(curl -fsSIo /dev/null -w '%{redirect_url}' "$repository/releases/latest")"
version="${latest##*/tag/v}"
if [ -z "$version" ] || [ "$version" = "$latest" ]; then
  echo "could not resolve the latest opeco release from $repository/releases/latest (got '$latest')" >&2
  exit 1
fi
if [ "$(printf '%s\n%s\n' "$minimum_version" "$version" | sort -V | head -n1)" != "$minimum_version" ]; then
  echo "latest opeco release is $version but isotty needs at least $minimum_version" >&2
  exit 1
fi
releases="$repository/releases/download/v${version}"
raw="https://raw.githubusercontent.com/yacchin1205/opeco-link/v${version}"

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
env="{\"BROWSER\": \"$open_client\"}"

mkdir -p "$tmp/skill/references"
curl -fsSL -o "$tmp/skill/SKILL.md" "$raw/skills/opeco-link/SKILL.md"
curl -fsSL -o "$tmp/skill/references/security.md" "$raw/skills/opeco-link/references/security.md"
install_skill() {
  mkdir -p "$1/opeco-link"
  cp -R "$tmp/skill/." "$1/opeco-link/"
}

if [ "$install_claude" = 1 ]; then
  install_skill "$HOME/.claude/skills"
  curl -fsSL -o "$tmp/claude-hooks.json" "$raw/examples/agent-hooks/claude.json"
  python3 "$tmp/merge.py" mcp-json "$HOME/.claude.json" opeco-link "$env" /usr/local/bin/opeco --qr-port "$qr_port" mcp
  python3 "$tmp/merge.py" hooks "$HOME/.claude/settings.json" "$tmp/claude-hooks.json" opeco-link
fi

if [ "$install_codex" = 1 ]; then
  install_skill "$HOME/.codex/skills"
  curl -fsSL -o "$tmp/codex-hooks.json" "$raw/examples/agent-hooks/codex.json"
  python3 "$tmp/merge.py" mcp-toml "$HOME/.codex/config.toml" opeco-link "$env" /usr/local/bin/opeco --qr-port "$qr_port" mcp
  python3 "$tmp/merge.py" hooks "$HOME/.codex/hooks.json" "$tmp/codex-hooks.json" opeco-link
fi
