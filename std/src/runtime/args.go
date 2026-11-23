package runtime

// Process arguments via host imports (docs/design/07).
//
// getArgc / getArg are body-less go.runtime.* imports. getArg copies argv[i]
// into linear memory at ptr (up to max bytes) and returns the full byte
// length (so max=0 queries length). getArgString is filled by codegen: it
// calls getArg and copies host bytes into a Go string (reverse of printstring).

func getArgc() int32
func getArg(i, ptr, max int32) int32
func getArgString(i int32) string

// GetArgc returns the number of process arguments from the host.
func GetArgc() int { return int(getArgc()) }

// GetArg returns process argument i as a Go string.
func GetArg(i int) string { return getArgString(int32(i)) }
