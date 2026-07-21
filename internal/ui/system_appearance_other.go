//go:build !darwin && !linux

package ui

func detectSystemDarkMode() (dark, available bool) { return false, false }
