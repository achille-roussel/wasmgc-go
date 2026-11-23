// overlay of go1.27.4 encoding/binary/native_endian_little.go
// Upstream is tagged with wasm among little-endian archs; wasmgc is also LE.
package binary

type nativeEndian struct {
	littleEndian
}

// NativeEndian is the native-endian implementation of [ByteOrder] and [AppendByteOrder].
var NativeEndian nativeEndian
