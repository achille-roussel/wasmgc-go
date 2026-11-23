package runtime

// panicObj is $go.panicObj (docs/design/03). Declared so proc.g type-checks;
// EH codegen owns the recoverable panic path.
type panicObj struct {
	value     interface{}
	recovered int32
	next      *panicObj
}

// throw panics with s (recoverable channel/runtime errors).
func throw(s string) { panic(s) }

// fatal prints "fatal error: " + s and exits with status 2 (deadlock).
func fatal(s string) {
	printstring("fatal error: ")
	printstring(s)
	printnl()
	exit(2)
}
