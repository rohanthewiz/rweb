package rtr

// flow tells the main loop what it should do next.
// This type is used internally by the tree traversal algorithm to control
// the execution path without deep recursion or complex state management.
//
// Using an enum for control flow allows the tree operations to be implemented
// with a single loop and goto statements, improving performance by avoiding
// function call overhead.
type flow int

// Control flow values used during tree traversal.
// These direct the main loop in tree operations (Add, Lookup).
const (
	// flowStop indicates traversal should terminate (route fully processed)
	flowStop flow = iota
	
	// flowBegin indicates traversal should restart from the beginning of the loop
	// Used when switching to a parameter node that needs fresh traversal
	flowBegin
	
	// flowNext indicates traversal should continue to the next iteration
	// Used for normal progression through the tree
	flowNext
)
