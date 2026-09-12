package journal

import (
	"errors"
	"strings"
	"testing"

	"github.com/procrastivity/clast/internal/clasterr"
)

func itemFor(harness, nativeID string) WalkItem {
	return WalkItem{Key: SessionKey{Harness: harness, NativeID: nativeID}}
}

func asClastErr(t *testing.T, err error) *clasterr.Error {
	t.Helper()
	var ce *clasterr.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a *clasterr.Error", err)
	}
	return ce
}

func TestResolve_FullNameMatch(t *testing.T) {
	items := []WalkItem{itemFor("claude", "8f3a1111"), itemFor("codex", "2222")}

	got, err := Resolve(items, "claude-8f3a1111")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Key.DirName() != "claude-8f3a1111" {
		t.Errorf("resolved %q, want %q", got.Key.DirName(), "claude-8f3a1111")
	}
}

func TestResolve_UniquePrefixMatch(t *testing.T) {
	items := []WalkItem{itemFor("claude", "8f3a1111"), itemFor("codex", "2222")}

	got, err := Resolve(items, "claude-8f3a")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Key.DirName() != "claude-8f3a1111" {
		t.Errorf("resolved %q, want %q", got.Key.DirName(), "claude-8f3a1111")
	}
}

func TestResolve_ExactMatchBeatsPrefixCollision(t *testing.T) {
	// "claude-8f3a" is both a full name and a prefix of "claude-8f3abcd".
	items := []WalkItem{itemFor("claude", "8f3a"), itemFor("claude", "8f3abcd")}

	got, err := Resolve(items, "claude-8f3a")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Key.DirName() != "claude-8f3a" {
		t.Errorf("resolved %q, want the exact match %q", got.Key.DirName(), "claude-8f3a")
	}
}

func TestResolve_ZeroMatchesNotFound(t *testing.T) {
	items := []WalkItem{itemFor("claude", "8f3a1111")}

	_, err := Resolve(items, "codex-nope")
	if err == nil {
		t.Fatal("Resolve: want error, got nil")
	}
	ce := asClastErr(t, err)
	if ce.Code != "not-found.session" {
		t.Errorf("Code = %q, want %q", ce.Code, "not-found.session")
	}
	if !strings.Contains(ce.Message, "codex-nope") {
		t.Errorf("message %q does not name the locator", ce.Message)
	}
}

func TestResolve_MultipleMatchesAmbiguous(t *testing.T) {
	items := []WalkItem{itemFor("claude", "8f3a1111"), itemFor("claude", "8f3a2222")}

	_, err := Resolve(items, "claude-8f3a")
	if err == nil {
		t.Fatal("Resolve: want error, got nil")
	}
	ce := asClastErr(t, err)
	if ce.Code != "validation.ambiguous-locator" {
		t.Errorf("Code = %q, want %q", ce.Code, "validation.ambiguous-locator")
	}
	for _, want := range []string{"claude-8f3a1111", "claude-8f3a2222"} {
		if !strings.Contains(ce.Message, want) {
			t.Errorf("message %q does not name candidate %q", ce.Message, want)
		}
	}
}

func TestResolve_EmptyLocator(t *testing.T) {
	items := []WalkItem{itemFor("claude", "8f3a1111")}

	_, err := Resolve(items, "")
	if err == nil {
		t.Fatal("Resolve: want error, got nil")
	}
	ce := asClastErr(t, err)
	if ce.Code != "validation.empty-locator" {
		t.Errorf("Code = %q, want %q", ce.Code, "validation.empty-locator")
	}
}

func TestResolve_EmptyItemsNotFound(t *testing.T) {
	_, err := Resolve(nil, "claude-8f3a")
	if err == nil {
		t.Fatal("Resolve: want error, got nil")
	}
	ce := asClastErr(t, err)
	if ce.Code != "not-found.session" {
		t.Errorf("Code = %q, want %q", ce.Code, "not-found.session")
	}
}
