# Parameter Name Consistency in RWeb Router

## Overview

The RWeb radix tree router enforces **parameter name consistency** at the same parameter position across different routes. This is a fundamental requirement of the radix tree data structure, not a limitation.

## The Requirement

When multiple routes share the same parameter position in the tree structure, they **must use the same parameter name** because they share the same parameter node internally.

## Examples

### ✓ Valid Routes

```go
s := rweb.NewServer()

// These routes are VALID - they use consistent parameter names at each position
s.Get("/users/:id", handler1)                    // First param: :id
s.Get("/users/:id/profile", handler2)            // First param: :id ✓
s.Get("/users/:id/posts", handler3)              // First param: :id ✓
s.Get("/users/:id/posts/:postId", handler4)      // First param: :id, Second param: :postId ✓
```

```go
// These routes are VALID - they use consistent parameter names
s.Get("/posts/:year/:title", handler1)           // First param: :year, Second param: :title
s.Get("/posts/:year/posts/:postId", handler2)    // First param: :year (matches!) ✓
```

### ✗ Invalid Routes (Will Panic)

```go
s := rweb.NewServer()

// These routes are INVALID - conflicting parameter names at the same position
s.Get("/users/:id", handler1)                    // First param: :id
s.Get("/users/:userId/profile", handler2)        // First param: :userId ✗ PANIC!
// Error: "Existing parameter 'id' conflicts with new parameter 'userId'"
```

```go
// These routes are INVALID - conflicting parameter names at second position
s.Get("/posts/:year/:title", handler1)           // Second param: :title
s.Get("/posts/:year/:slug", handler2)            // Second param: :slug ✗ PANIC!
// Error: "Existing parameter 'title' conflicts with new parameter 'slug'"
```

## Why This Restriction Exists

In a radix tree, routes are organized in a tree structure where common prefixes are shared. When you register:

```
/users/:id
/users/:id/profile
```

The router creates this tree:
```
root
 └── "/users/"
      └── parameter node (name: "id")
           ├── data: handler for /users/:id
           └── "/profile"
                └── data: handler for /users/:id/profile
```

Both routes **share the same parameter node**. If you try to register `/users/:userId/posts`, the router would need to use the existing parameter node (which expects `:id`), but you're asking it to use `:userId`. This is impossible without breaking the first route.

## Parameter Independence

Parameters at **different tree depths** can use different names because they don't share nodes:

### ✓ Valid - Different Branches

```go
// These are valid because /api/v1/ and /api/v2/ create different branches
s.Get("/api/v1/:id", handler1)                   // Branch 1: param named :id
s.Get("/api/v2/:userId", handler2)               // Branch 2: param named :userId ✓
s.Get("/api/v3/:resourceId", handler3)           // Branch 3: param named :resourceId ✓
```

### ✓ Valid - Different Static Prefixes

```go
// These are valid because /admin/ and /user/ create different branches
s.Get("/admin/:userId", adminHandler)            // admin branch: :userId
s.Get("/user/:profileId", userHandler)           // user branch: :profileId ✓
```

## Best Practices

### 1. Use Descriptive, Consistent Parameter Names

Choose parameter names that accurately describe what they represent and use them consistently:

```go
// Good: Consistent and descriptive
s.Get("/users/:userId", getUser)
s.Get("/users/:userId/posts", getUserPosts)
s.Get("/users/:userId/profile", getUserProfile)
s.Get("/users/:userId/settings", getUserSettings)
```

### 2. Plan Your API Structure

Before implementing routes, plan which parameters will be at which positions:

```go
// API v1 - all resource endpoints use :resourceId at first parameter position
api := s.Group("/api/v1")
api.Get("/users/:resourceId", handler1)
api.Get("/users/:resourceId/posts", handler2)
api.Get("/posts/:resourceId", handler3)
api.Get("/posts/:resourceId/comments", handler4)
```

### 3. Use Route Groups for API Versioning

Different API versions can use different parameter names because they're on different branches:

```go
v1 := s.Group("/api/v1")
v1.Get("/users/:id", v1GetUser)              // v1 uses :id

v2 := s.Group("/api/v2")
v2.Get("/users/:userId", v2GetUser)          // v2 can use :userId (different branch!)
```

### 4. Document Your Route Structure

Maintain documentation showing which parameter names are used at each position:

```go
// User API Routes
// Position 1: :userId (identifies the user)
// Position 2: :resourceId (identifies user's resource)
s.Get("/users/:userId", getUser)
s.Get("/users/:userId/posts/:resourceId", getUserPost)
s.Get("/users/:userId/comments/:resourceId", getUserComment)
```

## Error Messages

When you register a route with a conflicting parameter name, you'll get a clear error:

```
panic: radix tree router: conflicting parameter names at the same position.
Existing parameter 'id' conflicts with new parameter 'userId'.
Routes sharing the same parameter position must use the same parameter name
because they share the same node in the tree structure.
```

This error includes:
- The existing parameter name at that position
- The conflicting new parameter name you tried to use
- An explanation of why this isn't allowed

## Migration Strategy

If you have existing routes with inconsistent parameter names, here's how to fix them:

### Step 1: Identify Conflicts

Run your server startup and look for panic messages about conflicting parameters.

### Step 2: Choose Standard Names

Decide on standard parameter names for each parameter position:
- Position 1: `:id` or `:userId` or `:resourceId`
- Position 2: `:postId`, `:commentId`, etc.

### Step 3: Update Routes

Update all routes at the same position to use the same parameter name:

```go
// Before (will panic)
s.Get("/users/:id/profile", handler1)
s.Get("/users/:userId/posts", handler2)  // ✗ Conflict!

// After (works)
s.Get("/users/:id/profile", handler1)
s.Get("/users/:id/posts", handler2)      // ✓ Consistent!
```

### Step 4: Update Handlers

Update your handlers to use the new parameter names:

```go
// Before
func handler(ctx rweb.Context) error {
    userId := ctx.Request().Param("userId")
    // ...
}

// After
func handler(ctx rweb.Context) error {
    userId := ctx.Request().Param("id")  // Changed to match route
    // ...
}
```

## Technical Details

The validation occurs in two places:

1. **During route traversal** (`treeNode.end()`): When the router is adding a new route and encounters an existing parameter node, it validates that the new route uses the same parameter name.

2. **During route appending** (`treeNode.append()`): When creating new parameter nodes, if a parameter node already exists at that position, it validates name consistency before reusing it.

Both validation points ensure that parameter name conflicts are caught immediately at route registration time, not at request handling time.

## Summary

- ✓ Routes sharing the same parameter position must use the same parameter name
- ✓ Parameters at different tree depths (different branches) can use different names
- ✓ Validation happens at route registration time (immediate feedback)
- ✓ Clear error messages explain exactly what's wrong
- ✓ This is a fundamental requirement of the radix tree structure, not a bug

By following these guidelines, you'll avoid parameter name conflicts and create a clean, consistent API structure.