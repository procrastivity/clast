package registry

import "testing"

func TestTableCarriesClaude(t *testing.T) {
	if len(Names) != 1 || Names[0] != "claude" {
		t.Fatalf("Names = %v, want [claude]", Names)
	}
	s, ok := Lookup("claude")
	if !ok || s.Name() != "claude" {
		t.Fatalf("Lookup(claude) = %v, %v", s, ok)
	}
	if _, ok := Lookup("devin"); ok {
		t.Fatal("Lookup(devin) should report false until the source lands")
	}
}
