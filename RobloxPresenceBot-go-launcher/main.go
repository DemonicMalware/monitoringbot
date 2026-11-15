package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func main() {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "launcher: no se pudo localizar el ejecutable:", err)
		os.Exit(1)
	}

	target := filepath.Join(filepath.Dir(executable), "presence-bot")
	if err := os.Chmod(target, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "launcher: no se pudo hacer ejecutable presence-bot:", err)
		os.Exit(1)
	}

	args := append([]string{target}, os.Args[1:]...)
	if err := syscall.Exec(target, args, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "launcher: no se pudo iniciar presence-bot:", err)
		os.Exit(1)
	}
}
