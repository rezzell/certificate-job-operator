package utils

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetNonEmptyLines(t *testing.T) {
	got := GetNonEmptyLines("one\n\ntwo\n\nthree\n")
	want := []string{"one", "two", "three"}

	if len(got) != len(want) {
		t.Fatalf("unexpected length: got %d want %d", len(got), len(want))
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected line %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestUncommentCode(t *testing.T) {
	const (
		prefix = "// "
		target = "// first line\n// second line"
	)

	t.Run("rewrites the target block", func(t *testing.T) {
		path := writeTempFile(t, "before\n"+target+"\nafter\n")

		if err := UncommentCode(path, target, prefix); err != nil {
			t.Fatalf("UncommentCode returned error: %v", err)
		}

		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile returned error: %v", err)
		}

		want := "before\nfirst line\nsecond line\nafter\n"
		if string(got) != want {
			t.Fatalf("unexpected file contents: got %q want %q", string(got), want)
		}
	})

	t.Run("fails when writing the prefix-free content fails", func(t *testing.T) {
		restore := stubBufferWrites(t, 2, 0)
		defer restore()

		path := writeTempFile(t, "before\n"+target+"\nafter\n")
		err := UncommentCode(path, target, prefix)
		if err == nil || !strings.Contains(err.Error(), "failed to write to output") {
			t.Fatalf("expected write error, got %v", err)
		}
	})

	t.Run("fails when writing the newline fails", func(t *testing.T) {
		restore := stubBufferWrites(t, 0, 2)
		defer restore()

		path := writeTempFile(t, "before\n"+target+"\nafter\n")
		err := UncommentCode(path, target, prefix)
		if err == nil || !strings.Contains(err.Error(), "failed to write to output") {
			t.Fatalf("expected write error, got %v", err)
		}
	})

	t.Run("fails when writing the leading content fails", func(t *testing.T) {
		restore := stubBufferWrites(t, 1, 0)
		defer restore()

		path := writeTempFile(t, "before\n"+target+"\nafter\n")
		err := UncommentCode(path, target, prefix)
		if err == nil || !strings.Contains(err.Error(), "failed to write to output") {
			t.Fatalf("expected write error, got %v", err)
		}
	})

	t.Run("fails when writing the trailing content fails", func(t *testing.T) {
		restore := stubBufferWrites(t, 0, 3)
		defer restore()

		path := writeTempFile(t, "before\n"+target+"\nafter\n")
		err := UncommentCode(path, target, prefix)
		if err == nil || !strings.Contains(err.Error(), "failed to write to output") {
			t.Fatalf("expected write error, got %v", err)
		}
	})

	t.Run("fails when the target is missing", func(t *testing.T) {
		path := writeTempFile(t, "before\nafter\n")

		err := UncommentCode(path, target, prefix)
		if err == nil || !strings.Contains(err.Error(), "unable to find the code") {
			t.Fatalf("expected missing target error, got %v", err)
		}
	})

	t.Run("fails when the source file is missing", func(t *testing.T) {
		err := UncommentCode(filepath.Join(t.TempDir(), "missing.txt"), target, prefix)
		if err == nil || !strings.Contains(err.Error(), "failed to read file") {
			t.Fatalf("expected read error, got %v", err)
		}
	})
}

func writeTempFile(t *testing.T, contents string) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "input.txt")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	return path
}

func stubBufferWrites(t *testing.T, failWriteOn, failWriteStringOn int) func() {
	t.Helper()

	origWrite := bufferWrite
	origWriteString := bufferWriteString

	var writeCalls int
	var writeStringCalls int

	bufferWrite = func(buf *bytes.Buffer, p []byte) (int, error) {
		writeCalls++
		if failWriteOn > 0 && writeCalls == failWriteOn {
			return 0, errors.New("write failed")
		}
		return buf.Write(p)
	}

	bufferWriteString = func(buf *bytes.Buffer, s string) (int, error) {
		writeStringCalls++
		if failWriteStringOn > 0 && writeStringCalls == failWriteStringOn {
			return 0, errors.New("write string failed")
		}
		return buf.WriteString(s)
	}

	return func() {
		bufferWrite = origWrite
		bufferWriteString = origWriteString
	}
}
