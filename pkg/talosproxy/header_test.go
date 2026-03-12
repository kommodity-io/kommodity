package talosproxy_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/talosproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteTargetAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		address string
	}{
		{
			name:    "standard address with port",
			address: "10.200.0.5:50000",
		},
		{
			name:    "ipv6 address with port",
			address: "[::1]:50000",
		},
		{
			name:    "empty address",
			address: "",
		},
		{
			name:    "hostname with port",
			address: "node1.example.com:50000",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			err := talosproxy.WriteTargetAddress(&buf, testCase.address)
			require.NoError(t, err)

			// Read length prefix
			var length uint32

			err = binary.Read(&buf, binary.BigEndian, &length)
			require.NoError(t, err)

			//nolint:gosec // test string length will never overflow uint32
			assert.Equal(t, uint32(len(testCase.address)), length)

			// Read address
			addressBytes := make([]byte, length)
			_, err = buf.Read(addressBytes)
			require.NoError(t, err)
			assert.Equal(t, testCase.address, string(addressBytes))

			// Buffer should be empty
			assert.Equal(t, 0, buf.Len())
		})
	}
}

func TestWriteTargetAddressFormat(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	address := "10.200.0.5:50000"

	err := talosproxy.WriteTargetAddress(&buf, address)
	require.NoError(t, err)

	// Verify exact byte layout: [0x00 0x00 0x00 0x10] + "10.200.0.5:50000"
	data := buf.Bytes()
	expectedLen := 4 + len(address)
	assert.Len(t, data, expectedLen)

	// First 4 bytes are the length in big-endian
	assert.Equal(t, byte(0x00), data[0])
	assert.Equal(t, byte(0x00), data[1])
	assert.Equal(t, byte(0x00), data[2])

	//nolint:gosec // test string length will never overflow uint32
	assert.Equal(t, byte(uint32(len(address))), data[3])

	// Remaining bytes are the address string
	assert.Equal(t, address, string(data[4:]))
}
