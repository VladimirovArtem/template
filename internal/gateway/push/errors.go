package push

import "fmt"

type HTTPError struct {
	StatusCode int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("push service returned HTTP %d", e.StatusCode)
}

func (e *HTTPError) HTTPStatus() int { return e.StatusCode }
