package rweb

// Handler is a function that deals with the given request/response context.
type Handler func(Context) error
