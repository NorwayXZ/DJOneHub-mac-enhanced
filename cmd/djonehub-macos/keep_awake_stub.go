//go:build !windows && (!darwin || !cgo)

package main

func setKeepAwake(bool) {}
