package ir

import "testing"

func TestIsReflectKind(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want bool
	}{
		{0, true},  // Invalid
		{1, true},  // Bool
		{17, true}, // Array
		{23, true}, // Slice
		{26, true}, // UnsafePointer
		{27, false},
		{88, false},  // 'X'
		{113, false}, // 'q'
		{115, false}, // 's'
		{120, false}, // 'x'
		{-1, false},
	} {
		if g := isReflectKind(tc.n); g != tc.want {
			t.Errorf("isReflectKind(%d)=%v want %v", tc.n, g, tc.want)
		}
	}
}

func TestAllOrKindsDeadIgnoresVerbRunes(t *testing.T) {
	// i32.or (i32.eq tag, 115) (i32.eq tag, 113) — fmt verb 's'|'q'
	tag := &Name{Raw: "tag"}
	eq := func(n int64) Expr {
		return &Instr{Op: OpI32Eq, Args: []Expr{tag, &Const{Op: OpI32Const, I64: n}}}
	}
	cond := &Instr{Op: OpI32Or, Args: []Expr{eq(115), eq(113), eq(120), eq(88)}}
	live := map[int64]bool{1: true} // only Bool live — irrelevant for verbs
	if allOrKindsDead(cond, live) {
		t.Fatal("verb or-chain must not be treated as dead kind cascade")
	}
}

func TestAllOrKindsDeadClearsDeadKinds(t *testing.T) {
	tag := &Name{Raw: "tag"}
	eq := func(n int64) Expr {
		return &Instr{Op: OpI32Eq, Args: []Expr{tag, &Const{Op: OpI32Const, I64: n}}}
	}
	// Complex64|Complex128 with neither live
	cond := &Instr{Op: OpI32Or, Args: []Expr{eq(15), eq(16)}}
	live := map[int64]bool{1: true}
	if !allOrKindsDead(cond, live) {
		t.Fatal("dead kind or-chain should clear")
	}
	live[15] = true
	if allOrKindsDead(cond, live) {
		t.Fatal("live kind in or-chain must keep arm")
	}
}
