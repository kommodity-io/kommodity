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

// closeWriter is implemented by connections that support half-close (e.g., *net.TCPConn, trackedConn).
type closeWriter interface {
	CloseWrite() error
}

// bidirectionalCopy copies data between two connections in both directions.
// It waits for both directions to complete before returning.
func bidirectionalCopy(clientConn net.Conn, tunnel net.Conn) {
	var waitGroup sync.WaitGroup

	waitGroup.Add(bidirectionalCopyCount)

	go func() {
		defer waitGroup.Done()

		_, _ = io.Copy(tunnel, clientConn)
		// Signal write-half close if supported
		if cw, ok := tunnel.(closeWriter); ok {
			_ = cw.CloseWrite()
		}
	}()

	go func() {
		defer waitGroup.Done()

		_, _ = io.Copy(clientConn, tunnel)
		// Signal write-half close if supported
		if cw, ok := clientConn.(closeWriter); ok {
			_ = cw.CloseWrite()
		}
	}()

	waitGroup.Wait()
}
