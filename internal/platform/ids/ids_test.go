package ids

import "testing"

func TestDeriveIsStableSeparatedAndValid(t *testing.T) {
	parent := "10000000-0000-4000-8000-000000000001"
	first, err := Derive(parent, "turn/1/invocation")
	if err != nil || Validate(first) != nil {
		t.Fatalf("derived=%q err=%v", first, err)
	}
	repeated, _ := Derive(parent, "turn/1/invocation")
	second, _ := Derive(parent, "turn/2/invocation")
	if first != repeated || first == second || first[14] != '8' {
		t.Fatalf("first=%q repeated=%q second=%q", first, repeated, second)
	}
	if _, err := Derive("bad", "turn"); err == nil {
		t.Fatal("invalid namespace was accepted")
	}
}
