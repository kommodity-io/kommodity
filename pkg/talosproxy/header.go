package talosproxy

import (
	"encoding/binary"
	"fmt"
	"io"
)

// WriteTargetAddress writes the target address header to the writer using the
// talos-cluster-proxy protocol: 4-byte big-endian uint32 length followed by the address string.
func WriteTargetAddress(writer io.Writer, address string) error {
	length := uint32(len(address)) //nolint:gosec // len(address) for a network address string will never overflow uint32

	err := binary.Write(writer, binary.BigEndian, length)
	if err != nil {
		return fmt.Errorf("failed to write target address length: %w", err)
	}

	_, err = io.WriteString(writer, address)
	if err != nil {
		return fmt.Errorf("failed to write target address: %w", err)
	}

	return nil
}
