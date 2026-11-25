set -euo pipefail

merge="__MERGE_SCRIPT__"

python3 "$merge" mcp-json-remove "$HOME/.claude.json" opeco-link
python3 "$merge" hooks-remove "$HOME/.claude/settings.json" opeco-link
python3 "$merge" mcp-toml-remove "$HOME/.codex/config.toml" opeco-link
python3 "$merge" hooks-remove "$HOME/.codex/hooks.json" opeco-link
rm -rf "$HOME/.claude/skills/opeco-link" "$HOME/.codex/skills/opeco-link"
session_dir="__SESSION_DIR__"
session_client="__SESSION_CLIENT__"
boot_unit=/etc/systemd/system/isotty-opeco-boot.service
__UNPERSIST__
sudo rm -f /usr/local/bin/opeco /usr/local/share/isotty/opeco-version
