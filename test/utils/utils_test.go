package utils

import (
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
		path := writeTempFile(t, "before\n"+target+"\nafter\n")
		err := uncommentCodeWithWriter(path, target, prefix, failingWriterFactory(2, 0))
		if err == nil || !strings.Contains(err.Error(), "failed to write to output") {
			t.Fatalf("expected write error, got %v", err)
		}
	})

	t.Run("fails when writing the newline fails", func(t *testing.T) {
		path := writeTempFile(t, "before\n"+target+"\nafter\n")
		err := uncommentCodeWithWriter(path, target, prefix, failingWriterFactory(0, 2))
		if err == nil || !strings.Contains(err.Error(), "failed to write to output") {
			t.Fatalf("expected write error, got %v", err)
		}
	})

	t.Run("fails when writing the leading content fails", func(t *testing.T) {
		path := writeTempFile(t, "before\n"+target+"\nafter\n")
		err := uncommentCodeWithWriter(path, target, prefix, failingWriterFactory(1, 0))
		if err == nil || !strings.Contains(err.Error(), "failed to write to output") {
			t.Fatalf("expected write error, got %v", err)
		}
	})

	t.Run("fails when writing the trailing content fails", func(t *testing.T) {
		path := writeTempFile(t, "before\n"+target+"\nafter\n")
		err := uncommentCodeWithWriter(path, target, prefix, failingWriterFactory(0, 3))
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

type failingWriter struct {
	content          []byte
	writeCalls       int
	writeStringCalls int
	failWriteOn      int
	failWriteStrOn   int
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.writeCalls++
	if w.failWriteOn > 0 && w.writeCalls == w.failWriteOn {
		return 0, errors.New("write failed")
	}
	w.content = append(w.content, p...)
	return len(p), nil
}

func (w *failingWriter) WriteString(s string) (int, error) {
	w.writeStringCalls++
	if w.failWriteStrOn > 0 && w.writeStringCalls == w.failWriteStrOn {
		return 0, errors.New("write string failed")
	}
	w.content = append(w.content, s...)
	return len(s), nil
}

func (w *failingWriter) Bytes() []byte {
	return w.content
}

func failingWriterFactory(failWriteOn, failWriteStringOn int) func() outputWriter {
	return func() outputWriter {
		return &failingWriter{
			failWriteOn:    failWriteOn,
			failWriteStrOn: failWriteStringOn,
		}
	}
}
