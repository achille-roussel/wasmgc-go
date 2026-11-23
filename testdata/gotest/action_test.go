package gotest

import "testing"

func TestParseActionRun(t *testing.T) {
	a, err := ParseAction("// run\n\npackage main\n")
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != "run" {
		t.Fatalf("kind %q", a.Kind)
	}
}

func TestParseActionErrorcheck(t *testing.T) {
	a, err := ParseAction("// errorcheck\n\npackage main\n")
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != "errorcheck" || !a.WantErr {
		t.Fatalf("%+v", a)
	}
}

func TestParseSkipBytes(t *testing.T) {
	entries, err := ParseSkipBytes([]byte("# c\nhelloworld.go — output mismatch\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "helloworld.go" || entries[0].Reason != "output mismatch" {
		t.Fatalf("%+v", entries)
	}
}

func TestParseSkipBytesNoReason(t *testing.T) {
	_, err := ParseSkipBytes([]byte("helloworld.go\n"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCategorizeFailure(t *testing.T) {
	if g, w := CategorizeFailure("m: lower: range over func(yield func() bool) not supported"), "range-over-func"; g != w {
		t.Fatalf("%q want %q", g, w)
	}
	if g, w := CategorizeFailure("output does not match expected"), "output mismatch"; g != w {
		t.Fatalf("%q want %q", g, w)
	}
}
