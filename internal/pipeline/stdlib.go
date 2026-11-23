package pipeline

import (
	"context"
	"fmt"
	"os"

	"github.com/achille-roussel/wasmgc-go/internal/stdlib"
)

// stdlibBackend adapts stream H's internal/stdlib to the Stdlib interface.
type stdlibBackend struct {
	// ModuleRoot overrides auto-detection from the working directory.
	ModuleRoot string
}

func (s *stdlibBackend) paths() (stdlib.Paths, error) {
	root := s.ModuleRoot
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return stdlib.Paths{}, err
		}
		root, err = stdlib.FindModuleRoot(wd)
		if err != nil {
			return stdlib.Paths{}, err
		}
	}
	return stdlib.DefaultPaths(root)
}

func (s *stdlibBackend) GOROOT(ctx context.Context) (string, error) {
	_ = ctx
	p, err := s.paths()
	if err != nil {
		return "", err
	}
	m, err := stdlib.Materialize(p)
	if err != nil {
		return "", err
	}
	return m.GOROOT, nil
}

func (s *stdlibBackend) Check(ctx context.Context) error {
	_ = ctx
	// Full reporting lives in cmd/wasmgc-go std check; this hook lets the
	// pipeline ask H to materialize and confirm the pin is coherent.
	p, err := s.paths()
	if err != nil {
		return err
	}
	m, err := stdlib.Materialize(p)
	if err != nil {
		return err
	}
	if m.Pin.Version == "" {
		return fmt.Errorf("stdlib: empty pin after materialize")
	}
	return nil
}
