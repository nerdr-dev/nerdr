package prompts

import (
	"os"
	"reflect"
	"testing"
)

func TestSaveAndLoad(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if ps, err := Load(); err != nil || ps != nil {
		t.Fatalf("no file is no prompts: %v %v", ps, err)
	}
	want := []Prompt{
		{Text: "commit, push and open a pull request", Enter: true},
		{Text: "line one\nline \"two\""},
	}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("read back %+v, %v", got, err)
	}
}

func TestLoadByHand(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Save(nil); err != nil { // makes the folder
		t.Fatal(err)
	}
	text := "[[prompt]]\ntext = \"  run the tests  \"\n\n[[prompt]]\ntext = \"\"\n"
	if err := os.WriteFile(Path(), []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil || len(got) != 1 || got[0].Text != "run the tests" || got[0].Enter {
		t.Fatalf("written by hand, enter left out and an empty one: %+v, %v", got, err)
	}
	if err := os.WriteFile(Path(), []byte("[[prompt]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("a file that does not read is an error")
	}
}
