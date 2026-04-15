package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadBatchFile(t *testing.T) {
	body := "# header comment\n" +
		"\n" +
		"https://www.bilibili.com/video/BV1xx411c7mD\n" +
		"   # indented comment\n" +
		"BV1yy411c7mE  # trailing comment\n" +
		"https://www.bilibili.com/video/BV1xx411c7mD\n" + // duplicate
		"\n" +
		"av170001\n"

	dir := t.TempDir()
	path := filepath.Join(dir, "urls.txt")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := readBatchFile(path)
	if err != nil {
		t.Fatalf("readBatchFile: %v", err)
	}
	want := []string{
		"https://www.bilibili.com/video/BV1xx411c7mD",
		"BV1yy411c7mE",
		"av170001",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestReadBatchFileMissing(t *testing.T) {
	_, err := readBatchFile(filepath.Join(t.TempDir(), "nope.txt"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadBatchFileEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, []byte("# only comments\n\n   \n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readBatchFile(path)
	if err != nil {
		t.Fatalf("readBatchFile: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}
