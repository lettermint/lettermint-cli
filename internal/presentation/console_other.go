//go:build !windows

package presentation

import "io"

func enableANSI(io.Writer) func() { return func() {} }
