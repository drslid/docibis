package render

// Request asks a worker to convert one file. Paths are absolute.
type Request struct {
	ID     int    `json:"id"`
	In     string `json:"in"`
	Out    string `json:"out"`
	Format string `json:"format"`
	Filter string `json:"filter,omitempty"`
}

// Reply answers a request; the first reply of a worker says it is ready.
type Reply struct {
	ID    int    `json:"id,omitempty"`
	Ready bool   `json:"ready,omitempty"`
	Ms    int64  `json:"ms"`
	Error string `json:"error,omitempty"`
}
