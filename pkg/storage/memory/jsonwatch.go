package memory

import "k8s.io/apimachinery/pkg/watch"

const watchBufferSize = 10

// jsonWatch represents a watch on a JSON BLOB resource.
type jsonWatch struct {
	j  *jsonblobREST
	id int
	ch chan watch.Event
}

// newJSONWatch creates a new jsonWatch instance.
func newJSONWatch(j *jsonblobREST, id int) *jsonWatch {
	return &jsonWatch{
		j:  j,
		id: id,
		// Buffered channel to avoid blocking
		ch: make(chan watch.Event, watchBufferSize),
	}
}

// Stop stops the watch and closes the channel.
func (w *jsonWatch) Stop() {
	w.j.muWatchers.Lock()
	defer w.j.muWatchers.Unlock()

	if _, exists := w.j.watchers[w.id]; exists {
		delete(w.j.watchers, w.id)
		close(w.ch)
	}
}

// ResultChan returns the channel for receiving watch events.
func (w *jsonWatch) ResultChan() <-chan watch.Event {
	return w.ch
}
