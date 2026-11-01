package http

// Client wraps net/http with a fixed timeout.
type Client struct{ TimeoutSeconds int }
