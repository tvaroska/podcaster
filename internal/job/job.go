package job

import "context"

// Dispatcher starts asynchronous TTS work for an episode.
type Dispatcher interface {
	Enqueue(ctx context.Context, episodeID string) error
}
