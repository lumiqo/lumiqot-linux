package main

import (
	"context"
	_ "embed"
)

//go:embed lumiqot-librarian.service
var librarianUnitFile []byte

const librarianUnit = "lumiqot-librarian.service"

func librarianReady(_ context.Context, state unitState) (bool, error) {
	err := verifyServiceExecutable(state.pid, "Librarian", "../libexec/lumiqot/lumiqo-librarian")
	return err == nil, err
}

func startLibrarian(ctx context.Context) error {
	if err := startUnit(ctx, librarianUnit, librarianUnitFile); err != nil {
		return err
	}
	if err := waitForUnitReady(ctx, librarianUnit, librarianReady); err != nil {
		return err
	}
	_, err := systemctl(ctx, "enable", "--", librarianUnit)
	return err
}
