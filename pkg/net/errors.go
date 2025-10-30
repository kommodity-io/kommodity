// Package net provides error definitions for network utilities.
package net

import "errors"

var (
	// ErrIPRequired is returned when the IP address is missing from the request.
	ErrIPRequired = errors.New("IP address is required")
	// ErrNoMachineFound is returned when no machine is found for the given criteria.
	ErrNoMachineFound = errors.New("no machine found")
)
