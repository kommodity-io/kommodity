package storage

import "k8s.io/apimachinery/pkg/watch"

const watchBufferSize = 10

// storageWatch represents a watch on a storage resource.
type storageWatch struct {
	s  *storageREST
	id int
	ch chan watch.Event
}

// newStorageWatch creates a new storageWatch instance.
func newStorageWatch(s *storageREST, id int) *storageWatch {
	return &storageWatch{
		s:  s,
		id: id,
		// Buffered channel to avoid blocking
		ch: make(chan watch.Event, watchBufferSize),
	}
}

// Stop stops the watch and closes the channel.
func (w *storageWatch) Stop() {
	w.s.muWatchers.Lock()
	defer w.s.muWatchers.Unlock()

	if _, exists := w.s.watchers[w.id]; exists {
		delete(w.s.watchers, w.id)
		close(w.ch)
	}
}

// ResultChan returns the channel for receiving watch events.
func (w *storageWatch) ResultChan() <-chan watch.Event {
	return w.ch
}
