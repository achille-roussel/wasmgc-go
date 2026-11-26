package runtime

// Process arguments via host imports (docs/design/07).
//
// getArgc / getArg are body-less go.runtime.* imports. getArg copies argv[i]
// into linear memory at ptr (up to max bytes) and returns the full byte
// length (so max=0 queries length). getArgString is filled by codegen: it
// calls getArg and copies host bytes into a Go string (reverse of printstring).
//
// getEnv(keyPtr, keyLen, outPtr, max) copies process.env[key] the same way
// and returns -1 when the key is absent. getEnvString is filled by codegen.

func getArgc() int32
func getArg(i, ptr, max int32) int32
func getArgString(i int32) string
func getEnv(keyPtr, keyLen, outPtr, max int32) int32
func getEnvString(key string) (string, bool)

// GetArgc returns the number of process arguments from the host.
func GetArgc() int { return int(getArgc()) }

// GetArg returns process argument i as a Go string.
func GetArg(i int) string { return getArgString(int32(i)) }

// Getenv looks up key in the host environment (process.env on Node).
func Getenv(key string) (string, bool) { return getEnvString(key) }
