package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

//go:embed lumiqot-librarian.service
var librarianUnitFile []byte

const librarianUnit = "lumiqot-librarian.service"

func librarianReady(ctx context.Context, state unitState) (bool, error) {
	if err := verifyServiceExecutable(state.pid, "Librarian", "../libexec/lumiqot/lumiqo-librarian"); err != nil {
		return false, err
	}
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(dir) {
		return false, fmt.Errorf("XDG_RUNTIME_DIR must be absolute")
	}
	dialer := net.Dialer{Timeout: 500 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "unix", filepath.Join(dir, "lumiqot-librarian/mcp.sock"))
	if err != nil {
		return false, err
	}
	conn.Close()
	return true, nil
}

func startLibrarian(ctx context.Context) error {
	if err := startUnit(ctx, librarianUnit, librarianUnitFile); err != nil {
		return err
	}
	if err := waitForUnitReady(ctx, librarianUnit, func(ctx context.Context, state unitState) (bool, error) {
		ready, err := librarianReady(ctx, state)
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
			// Type=exec becomes active before Librarian loads the store and binds.
			return false, nil
		}
		return ready, err
	}); err != nil {
		return err
	}
	_, err := systemctl(ctx, "enable", "--", librarianUnit)
	return err
}
