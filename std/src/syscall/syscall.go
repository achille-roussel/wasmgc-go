// Package syscall is a wasmgc rewrite: JS-shaped, ENOSYS for filesystem,
// Write on fd 1 and 2 via runtime.write (placeholder for std check).
package syscall

import "unsafe"

const (
	AF_INET  = 2
	SOCK_STREAM = 1
)

type Errno uintptr

func (e Errno) Error() string {
	if e == 0 {
		return "errno 0"
	}
	return "errno " + itoa(int(e))
}

func (e Errno) Is(target error) bool {
	return e == target
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	n := len(b)
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		b[n] = '-'
	}
	return string(b[n:])
}

var (
	EINVAL  = Errno(0x16)
	ENOSYS  = Errno(0x26)
	EAGAIN  = Errno(0xb)
	EACCES  = Errno(0xd)
	EEXIST  = Errno(0x11)
	ENOENT  = Errno(0x2)
	EPERM   = Errno(0x1)
	EPIPE   = Errno(0x20)
	EINTR   = Errno(0x4)
	EISDIR  = Errno(0x15)
	ENOTDIR = Errno(0x14)
)

// writeHost copies p through the linear bridge and calls go.runtime.write.
// Body-less: filled by codegen (same pattern as runtime.printstring).
func writeHost(fd int32, p []byte) int32

func Write(fd int, p []byte) (n int, err error) {
	if fd != 1 && fd != 2 {
		return 0, ENOSYS
	}
	return int(writeHost(int32(fd), p)), nil
}

func Read(fd int, p []byte) (n int, err error) {
	return 0, ENOSYS
}

func Open(path string, mode int, perm uint32) (fd int, err error) {
	return -1, ENOSYS
}

func Close(fd int) error { return ENOSYS }

func Getenv(key string) (value string, found bool) { return "", false }

func Environ() []string { return nil }

func Getwd() (string, error) { return "", ENOSYS }

// Exit terminates the process. v1 panics with a marker the host may map to
// process.exit; prefer returning from main when possible.
func Exit(code int) { panic("syscall.Exit") }

func Getpid() int  { return 1 }
func Getppid() int { return 0 }
func Getuid() int  { return 0 }
func Getgid() int  { return 0 }
func Geteuid() int { return 0 }
func Getegid() int { return 0 }

func BytePtrFromString(s string) (*byte, error) {
	a := make([]byte, len(s)+1)
	copy(a, s)
	return &a[0], nil
}

func ByteSliceFromString(s string) ([]byte, error) {
	a := make([]byte, len(s)+1)
	copy(a, s)
	return a[0 : len(s)+1 : len(s)+1], nil
}

func StringByteSlice(s string) []byte {
	a, _ := ByteSliceFromString(s)
	return a
}

func SlicePtrFromStrings(ss []string) ([]*byte, error) {
	return nil, nil
}

type Stat_t struct {
	Dev  uint64
	Ino  uint64
	Mode uint32
	Size int64
}

func Stat(path string, st *Stat_t) error  { return ENOSYS }
func Lstat(path string, st *Stat_t) error { return ENOSYS }
func Fstat(fd int, st *Stat_t) error     { return ENOSYS }

func Fcntl(fd, cmd int, arg int) (int, error) { return 0, ENOSYS }

func Syscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err Errno) {
	return 0, 0, ENOSYS
}

func Syscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err Errno) {
	return 0, 0, ENOSYS
}

func RawSyscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err Errno) {
	return 0, 0, ENOSYS
}

func Pointer(p unsafe.Pointer) uintptr { return 0 }
