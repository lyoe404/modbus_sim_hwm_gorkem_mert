package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	p := write(t, "\ufeff# comment\r\nENVFILE_A=plain\r\nENVFILE_B=\"quoted value\"\r\n\r\nexport ENVFILE_C='single'\r\nENVFILE_D=a=b\r\n")
	for _, k := range []string{"ENVFILE_A", "ENVFILE_B", "ENVFILE_C", "ENVFILE_D"} {
		t.Setenv(k, "") // registers cleanup...
		os.Unsetenv(k)  // ...then start unset
	}
	if err := Load(p); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"ENVFILE_A": "plain",
		"ENVFILE_B": "quoted value",
		"ENVFILE_C": "single",
		"ENVFILE_D": "a=b",
	}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestExistingEnvironmentWins(t *testing.T) {
	t.Setenv("ENVFILE_E", "from-shell")
	if err := Load(write(t, "ENVFILE_E=from-file\n")); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("ENVFILE_E"); got != "from-shell" {
		t.Fatalf("got %q, shell value must win", got)
	}
}

func TestMissingFileIsFine(t *testing.T) {
	if err := Load(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedLine(t *testing.T) {
	if err := Load(write(t, "this is not valid\n")); err == nil {
		t.Fatal("malformed line must be reported")
	}
}
