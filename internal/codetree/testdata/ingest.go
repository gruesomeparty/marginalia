// Package ingest batches.
package ingest

import "fmt"

// batchCap is the cap.
const (
	batchCap = 10 // trailing
	other    = 2
)

const single = 1

var x, y int

type (
	A struct{ f int }
	B = int
)

// Batch holds.
type Batch[T any] struct {
	// Cap is the cap.
	Cap  int
	a, b string
	fmt.Stringer
}

type Doer interface {
	Do() error
}

// Full reports.
func (b *Batch[T]) Full() bool { return b.Cap > 0 }

func Ingest(x int) error {
	if x > 0 {
		return nil
	}
	return fmt.Errorf("x")
}

func broken( {
