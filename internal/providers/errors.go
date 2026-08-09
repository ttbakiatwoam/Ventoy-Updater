package providers

import (
	"errors"
	"fmt"
)

var ErrSkipped = errors.New("provider skipped")

func Skipf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrSkipped, fmt.Sprintf(format, args...))
}

func IsSkipped(err error) bool {
	return errors.Is(err, ErrSkipped)
}
