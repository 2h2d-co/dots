package dots

// ExitError carries an explicit process exit code for command errors.
type ExitError struct {
	Code   int
	Err    error
	Silent bool
}

// Error returns the wrapped error message.
func (e ExitError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// Unwrap returns the wrapped error.
func (e ExitError) Unwrap() error {
	return e.Err
}
