//go:build !windows

package service

import "context"

// IsWindowsService always returns false on non-Windows platforms.
func IsWindowsService() (bool, error) {
	return false, nil
}

// RunAsService is a no-op on non-Windows platforms.
func RunAsService(name string, runFunc func(ctx context.Context) error) error {
	return nil
}
