// Run unit tests stored in tests/ within their modules package using a Go overlay.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	if err := run(); err != nil {
		if failure, ok := err.(*exec.ExitError); ok {
			os.Exit(failure.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(root, "tests", "*_test.go"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("run from the Source root: no tests/*_test.go files found")
	}
	replacements := make(map[string]string, len(files))
	for _, source := range files {
		replacements[filepath.Join(root, "modules", filepath.Base(source))] = source
	}
	body, err := json.Marshal(struct{ Replace map[string]string }{replacements})
	if err != nil {
		return err
	}
	overlay, err := os.CreateTemp("", "discord-vlc-rpc-tests-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(overlay.Name())
	_, writeErr := overlay.Write(body)
	closeErr := overlay.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	operation, args := "test", os.Args[1:]
	if len(args) > 0 && args[0] == "vet" {
		operation, args = "vet", args[1:]
	}
	if len(args) == 0 {
		args = []string{"./..."}
	}
	args = append([]string{operation, "-overlay", overlay.Name()}, args...)
	command := exec.Command("go", args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	return command.Run()
}
