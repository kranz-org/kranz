//go:build !darwin && !linux

package ui

import (
	"errors"
	"io"
	"os"
)

func readTerminalBackground(_ *os.File, _ io.Writer) (string, error) {
	return "", errors.New("live terminal background detection is unsupported on this platform")
}
