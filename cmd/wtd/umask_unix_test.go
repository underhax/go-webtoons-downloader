//go:build unix

package main

import "testing"

func TestSetUmask(_ *testing.T) {
	setUmask()
}
