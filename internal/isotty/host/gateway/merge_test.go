package gateway

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// merge runs the merge script as the VM does, against files under dir.
func merge(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	script := filepath.Join(dir, "mcp-merge.py")
	if err := os.WriteFile(script, []byte(mergeScript), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("python3", append([]string{"-I", script}, args...)...).CombinedOutput()
	return string(output), err
}

func needPython(t *testing.T) {
	t.Helper()
	if err := exec.Command("python3", "-c", "import tomllib, tomli_w").Run(); err != nil {
		t.Skip("the merge script needs python3 with tomllib and tomli_w")
	}
}

// readTOML parses a TOML file into what JSON makes of it, so a test compares
// the data the agent will read and not the text.
func readTOML(t *testing.T, path string) map[string]any {
	t.Helper()
	out, err := exec.Command("python3", "-I", "-c", "import json, sys, tomllib\nwith open(sys.argv[1], 'rb') as f:\n    print(json.dumps(tomllib.load(f)))", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var data map[string]any
	if err := json.Unmarshal(out, &data); err != nil {
		t.Fatal(err)
	}
	return data
}

// A default setting is isotty's choice only where the human has made none:
// it is written once into an absent key and never over a value that is
// there, whatever it is, and every other value in the file stays.
func TestDefaultSettingsKeepWhatIsSet(t *testing.T) {
	needPython(t)
	dir := t.TempDir()

	settings := filepath.Join(dir, "claude", "settings.json")
	if out, err := merge(t, dir, "json-default", settings, "preferredNotifChannel", `"terminal_bell"`); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got, _ := os.ReadFile(settings); string(got) != "{\n  \"preferredNotifChannel\": \"terminal_bell\"\n}\n" {
		t.Fatalf("a missing file becomes the setting alone:\n%s", got)
	}
	if err := os.WriteFile(settings, []byte(`{"preferredNotifChannel": "iterm2", "theme": "dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := merge(t, dir, "json-default", settings, "preferredNotifChannel", `"terminal_bell"`); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got, _ := os.ReadFile(settings); string(got) != `{"preferredNotifChannel": "iterm2", "theme": "dark"}` {
		t.Fatalf("a set key is the human's, byte for byte:\n%s", got)
	}

	config := filepath.Join(dir, "codex", "config.toml")
	if out, err := merge(t, dir, "toml-default", config, "tui", "notification_method", `"bel"`); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got, want := readTOML(t, config), (map[string]any{"tui": map[string]any{"notification_method": "bel"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("a missing file becomes the table alone: %v", got)
	}
	// Whatever form the file takes, a set key stays and the rest of the
	// data is as it was.
	for _, existing := range []string{
		"model = \"o3\"\n\n[tui]\nnotification_method = \"osc9\"\n\n[projects.\"/workspace\"]\ntrust_level = \"trusted\"\n",
		"model = \"o3\"\ntui.notification_method = \"osc9\"\nprojects.\"/workspace\".trust_level = \"trusted\"\n",
		"model = \"o3\"\ntui = { notification_method = \"osc9\" }\n[projects.\"/workspace\"]\ntrust_level = \"trusted\"\n",
	} {
		if err := os.WriteFile(config, []byte(existing), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{
			{"notification_method", `"bel"`},
			{"notification_condition", `"always"`},
		} {
			if out, err := merge(t, dir, "toml-default", config, "tui", args[0], args[1]); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
		}
		want := map[string]any{
			"model":    "o3",
			"tui":      map[string]any{"notification_method": "osc9", "notification_condition": "always"},
			"projects": map[string]any{"/workspace": map[string]any{"trust_level": "trusted"}},
		}
		if got := readTOML(t, config); !reflect.DeepEqual(got, want) {
			t.Fatalf("from:\n%s\ngot %v\nwant %v", existing, got, want)
		}
	}
}
