//go:build !(darwin || linux || freebsd || netbsd || openbsd || dragonfly)

package board

func lockBoardFile(_ string) (func(), error) {
	return func() {}, nil
}
