//go:build !unix

package server

func diskUsedPercent(string) (float64, bool) { return 0, false }
