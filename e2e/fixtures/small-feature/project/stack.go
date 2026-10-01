// Package stack is a minimal last-in-first-out stack.
package stack

// Stack holds values in last-in-first-out order. The zero value is an empty
// stack ready to use.
type Stack[T any] struct {
	items []T
}

// Push adds v on top of the stack.
func (s *Stack[T]) Push(v T) {
	s.items = append(s.items, v)
}

// Pop removes and returns the top element. It returns the zero value and
// false when the stack is empty.
func (s *Stack[T]) Pop() (T, bool) {
	var zero T
	if len(s.items) == 0 {
		return zero, false
	}
	top := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return top, true
}

// Len returns the number of elements.
func (s *Stack[T]) Len() int {
	return len(s.items)
}
