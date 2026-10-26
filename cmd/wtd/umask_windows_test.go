//go:build windows

package main

import "testing"

func TestSetUmaskWindows(t *testing.T) {
	setUmask()
}
