package simulator

import "fmt"

// SimError is a custom error type for simulation errors
type SimError struct {
	Message string
}

func (e SimError) Error() string {
	return fmt.Sprintf("simulation error: %s", e.Message)
}

// ErrInvalidConfig creates an error for invalid configuration
func ErrInvalidConfig(msg string) error {
	return SimError{Message: fmt.Sprintf("invalid config: %s", msg)}
}
