package stack

import "testing"

func TestPushPop(t *testing.T) {
	var s Stack[int]
	s.Push(1)
	s.Push(2)
	if v, ok := s.Pop(); !ok || v != 2 {
		t.Fatalf("Pop() = %d, %v, want 2, true", v, ok)
	}
	if s.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", s.Len())
	}
}

func TestPeekReturnsTopWithoutRemoving(t *testing.T) {
	var s Stack[string]
	s.Push("a")
	s.Push("b")
	if v, ok := s.Peek(); !ok || v != "b" {
		t.Fatalf("Peek() = %q, %v, want \"b\", true", v, ok)
	}
	if s.Len() != 2 {
		t.Fatalf("Peek changed the stack: Len() = %d, want 2", s.Len())
	}
}

func TestPeekOnEmptyStack(t *testing.T) {
	var s Stack[int]
	if v, ok := s.Peek(); ok || v != 0 {
		t.Fatalf("Peek() on empty = %d, %v, want 0, false", v, ok)
	}
}
