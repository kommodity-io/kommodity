package talosproxy

import (
	"io"
	"net"
	"sync"
)

const (
	// bidirectionalCopyCount is the number of goroutines used for bidirectional copy.
	bidirectionalCopyCount = 2
)

// bidirectionalCopy copies data between two connections in both directions.
// It waits for both directions to complete before returning.
func bidirectionalCopy(clientConn net.Conn, tunnel net.Conn) {
	var waitGroup sync.WaitGroup

	waitGroup.Add(bidirectionalCopyCount)

	go func() {
		defer waitGroup.Done()

		_, _ = io.Copy(tunnel, clientConn)
		// Signal write-half close if supported
		if tc, ok := tunnel.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()

	go func() {
		defer waitGroup.Done()

		_, _ = io.Copy(clientConn, tunnel)
		// Signal write-half close if supported
		if tc, ok := clientConn.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}()

	waitGroup.Wait()
}
