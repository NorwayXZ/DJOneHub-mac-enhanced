//go:build !windows

package main

func isServiceInvocation() bool  { return false }
func prepareServiceEnvironment() {}
func runAsService(func()) bool   { return false }
func handleServiceCommand() bool { return false }
