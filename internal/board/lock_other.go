//go:build !(darwin || linux || freebsd || netbsd || openbsd || dragonfly)

package board

func lockFile(_ string) (func(), error) {
	return func() {}, nil
}
