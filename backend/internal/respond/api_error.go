package respond

type APIError struct {
	// Not rendered in the body — it is already the HTTP status line.
	Status int

	Code    string
	Message string
}

func (e *APIError) Error() string { return e.Message }
