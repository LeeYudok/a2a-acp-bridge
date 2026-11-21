//go:build !(darwin || linux || freebsd || netbsd || openbsd || dragonfly)

package main

func lockBoardFile(_ string) (func(), error) {
	return func() {}, nil
}
