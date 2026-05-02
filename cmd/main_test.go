package main

import (
	"sort"
	"testing"
)

func TestParseWatchNamespaces(t *testing.T) {
	t.Parallel()

	t.Run("empty input returns nil", func(t *testing.T) {
		t.Parallel()
		if got := parseWatchNamespaces("   "); got != nil {
			t.Fatalf("expected nil for empty input, got %v", got)
		}
	})

	t.Run("trims and deduplicates namespaces", func(t *testing.T) {
		t.Parallel()
		got := parseWatchNamespaces(" ns-a,ns-b, ns-a ,, ns-c ")
		if len(got) != 3 {
			t.Fatalf("expected 3 namespaces, got %d (%v)", len(got), got)
		}
		for _, ns := range []string{"ns-a", "ns-b", "ns-c"} {
			if _, ok := got[ns]; !ok {
				t.Fatalf("missing namespace %q in %v", ns, got)
			}
		}
	})
}

func TestWatchNamespaceCacheConfig(t *testing.T) {
	t.Run("WATCH_NAMESPACE takes precedence", func(t *testing.T) {
		t.Setenv("WATCH_NAMESPACE", "ns-a,ns-b")
		t.Setenv("POD_NAMESPACE", "pod-ns")

		got := watchNamespaceCacheConfig()
		if len(got) != 2 {
			t.Fatalf("expected 2 namespaces, got %d", len(got))
		}
		if _, ok := got["pod-ns"]; ok {
			t.Fatalf("expected POD_NAMESPACE to be ignored when WATCH_NAMESPACE is set")
		}
	})

	t.Run("falls back to POD_NAMESPACE", func(t *testing.T) {
		t.Setenv("WATCH_NAMESPACE", "")
		t.Setenv("POD_NAMESPACE", "pod-ns")

		got := watchNamespaceCacheConfig()
		if len(got) != 1 {
			t.Fatalf("expected one namespace, got %d", len(got))
		}
		if _, ok := got["pod-ns"]; !ok {
			t.Fatalf("expected pod-ns in result, got %v", got)
		}
	})

	t.Run("returns nil when env vars are empty", func(t *testing.T) {
		t.Setenv("WATCH_NAMESPACE", "")
		t.Setenv("POD_NAMESPACE", " ")

		if got := watchNamespaceCacheConfig(); got != nil {
			t.Fatalf("expected nil config, got %v", got)
		}
	})
}

func TestKeys(t *testing.T) {
	t.Parallel()

	in := parseWatchNamespaces("ns-b,ns-a")
	got := keys(in)
	sort.Strings(got)

	if len(got) != 2 || got[0] != "ns-a" || got[1] != "ns-b" {
		t.Fatalf("unexpected keys result: %v", got)
	}
}
