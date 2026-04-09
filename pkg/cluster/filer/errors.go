package filer

import "fmt"

// requiredErr formats a consistent error message used by backend
// validators when a mandatory field is missing.
func requiredErr(backend, field string) error {
	return fmt.Errorf("filer backend %s: missing required field %q", backend, field)
}
