package ir

import (
	"strings"
	"testing"
)

func TestScrubDeadGlobalStores_removesWriteOnlyInit(t *testing.T) {
	src := `low
type $go.array.i64 array mut i64

global mut dead.table refnull $go.array.i64
global mut live.flag i32

func export "run" main.run() {
  call pkg.init.0()
  call main.main()
  return
}

func main.main() {
  var x i32
  x = global.get live.flag
  return
}

func pkg.init.0() {
  var t refnull $go.array.i64
  %data = array.new_data $go.array.i64 $lit0
  t = %data
  global.set dead.table t
  global.set live.flag 1
  return
}

data $lit0 "\x01\x00\x00\x00\x00\x00\x00\x00"
`
	m, err := Parse("test.ir", src)
	if err != nil {
		t.Fatal(err)
	}
	ScrubDeadGlobalStores(m)
	PruneUnusedData(m)

	if m.LookupGlobal("dead.table") != nil {
		t.Fatal("dead.table should have been scrubbed")
	}
	if m.LookupGlobal("live.flag") == nil {
		t.Fatal("live.flag should remain")
	}
	text := Format(m)
	if strings.Contains(text, "global.set dead.table") {
		t.Fatalf("init still sets dead.table:\n%s", text)
	}
	if !strings.Contains(text, "global.set live.flag") {
		t.Fatalf("live.flag store missing:\n%s", text)
	}
}

func TestPruneNoopCalls_dropsPoolCleanup(t *testing.T) {
	src := `low
func export "run" main.run() {
  call sync.init.0()
  return
}

func sync.init.0() {
  call runtime.RegisterPoolCleanup(ref.func sync.poolCleanup)
  return
}

func sync.poolCleanup() {
  return
}

func runtime.RegisterPoolCleanup(cleanup refnull any) {
  return
}
`
	m, err := Parse("test.ir", src)
	if err != nil {
		t.Fatal(err)
	}
	PruneNoopCalls(m)
	PruneUnreachable(m)
	if m.LookupFunc("sync.poolCleanup") != nil {
		t.Fatal("poolCleanup should be unreachable after noop prune + DCE")
	}
}
