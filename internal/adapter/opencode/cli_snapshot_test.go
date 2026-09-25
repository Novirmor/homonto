package opencode_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/engine"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/tidwall/gjson"
)

func TestCLIMigrationSnapshotUndoRestoresLegacyStateWithoutRetargeting(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home, repo := t.TempDir(), t.TempDir()
	configPath := filepath.Join(repo, "homonto.toml")
	write := func(path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) string {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	write(configPath, "[tui.opencode]\ntheme = 'gruvbox'\n")
	tuiPath := filepath.Join(home, ".config", "opencode", "tui.json")
	cliPath := filepath.Join(home, ".config", "opencode", "cli.json")
	legacy := "{\n \"theme\": \"gruvbox\", \"diff_style\": \"stacked\"\n}\n"
	write(tuiPath, legacy)
	write(cliPath, `{"theme":{"mode":"dark"},"tabs":{"mode":"off"}}`)
	e, err := engine.Build(context.Background(), configPath, home, "homonto")
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }}
	e.State.Set("opencode", "tui.theme", `"gruvbox"`, secret.Hash(`"gruvbox"`))
	if err := e.State.Save(e.StateDir); err != nil {
		t.Fatal(err)
	}
	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	id, err := e.ApplySnapshot(context.Background(), sets)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.State.Get("opencode", "tui.theme"); ok {
		t.Fatal("migration did not retire legacy state")
	}
	if _, ok := e.State.Get("opencode", "tui.cli.theme.name"); !ok {
		t.Fatal("snapshot apply did not record V2 state")
	}
	if err := e.UndoSnapshot(id); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.State.Get("opencode", "tui.theme"); !ok {
		t.Fatal("undo did not restore the old state checkpoint")
	}
	if _, ok := e.State.Get("opencode", "tui.cli.theme.name"); ok {
		t.Fatal("undo retained the new state checkpoint")
	}
	cli := read(cliPath)
	if gjson.Get(cli, "theme.name").String() != "gruvbox" {
		t.Fatalf("current-TOML reverse planning changed behavior: %s", cli)
	}
	if read(tuiPath) != legacy || gjson.Get(cli, "theme.mode").String() != "dark" || gjson.Get(cli, "tabs.mode").String() != "off" {
		t.Fatal("undo changed V1 bytes or unmanaged CLI settings")
	}
	sets, err = e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	adopted, retired := false, false
	for _, cs := range sets {
		for _, ch := range cs.Changes {
			adopted = adopted || ch.Key == "tui.cli.theme.name" && ch.Action == "adopt"
			retired = retired || ch.Key == "tui.theme" && ch.Action == "delete"
		}
	}
	if !adopted || !retired {
		t.Fatalf("restored state not safely re-migrated: %+v", sets)
	}
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}
	if read(cliPath) != cli || read(tuiPath) != legacy {
		t.Fatal("re-adoption rewrote tool files")
	}
}
