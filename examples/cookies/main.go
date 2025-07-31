// Package main demonstrates cookie usage in rweb including sessions,
// flash messages, and remember me functionality.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/rohanthewiz/element"
	"github.com/rohanthewiz/rweb"
)

// simple in-memory session store (use Redis or similar in production)
var sessions = make(map[string]map[string]interface{})

// pageLayout is a reusable component for page structure
type pageLayout struct {
	Title string
	Body  element.Component
}

func (p pageLayout) Render(b *element.Builder) any {
	b.Html().R(
		b.Head().R(
			b.Title().T(p.Title),
			b.Style().T(`
				body { font-family: Arial, sans-serif; max-width: 800px; margin: 0 auto; padding: 20px; }
				.flash { padding: 10px; margin: 10px 0; border-radius: 5px; }
				.flash.success { background: #d4edda; color: #155724; }
				.flash.error { background: #f8d7da; color: #721c24; }
				form { margin: 20px 0; }
				input { margin: 5px 0; padding: 5px; }
				.info { background: #e9ecef; padding: 20px; border-radius: 5px; }
			`),
		),
		b.Body().R(
			element.RenderComponents(b, p.Body),
		),
	)
	return nil
}

// flashMessage component for displaying flash messages
type flashMessage struct {
	Type    string
	Message string
}

func (f flashMessage) Render(b *element.Builder) any {
	if f.Message != "" {
		b.DivClass("flash " + f.Type).T(f.Message)
	}
	return nil
}

func main() {
	s := rweb.NewServer(rweb.ServerOptions{
		Address: ":8080",
		Verbose: true,
		// set server-wide cookie defaults
		Cookie: rweb.CookieConfig{
			HttpOnly: true,                 // prevent JavaScript access by default
			SameSite: rweb.SameSiteLaxMode, // CSRF protection
			Path:     "/",                  // cookies available site-wide
		},
	})

	// session middleware - runs for all routes
	s.Use(sessionMiddleware)

	// home page
	s.Get("/", homeHandler)

	// login/logout
	s.Post("/login", loginHandler)
	s.Post("/logout", logoutHandler)

	// protected route - create a group with auth middleware
	protected := s.Group("/", requireAuth)
	protected.Get("/dashboard", dashboardHandler)

	// flash message demo
	s.Post("/flash-demo", flashDemoHandler)

	fmt.Println("Cookie example server starting on http://localhost:8080")
	fmt.Println("Try these endpoints:")
	fmt.Println("  GET  /              - Home page")
	fmt.Println("  POST /login         - Login (creates session)")
	fmt.Println("  GET  /dashboard     - Protected page (requires login)")
	fmt.Println("  POST /logout        - Logout (clears session)")
	fmt.Println("  POST /flash-demo    - Set a flash message")

	if err := s.Run(); err != nil {
		log.Fatal(err)
	}
}

// sessionMiddleware loads session data from cookie
func sessionMiddleware(ctx rweb.Context) error {
	if ctx.HasCookie("session_id") {
		sessionID, _ := ctx.GetCookie("session_id")
		if session, exists := sessions[sessionID]; exists {
			// store session data in context
			ctx.Set("session", session)
			ctx.Set("user_id", session["user_id"])
		}
	}
	return ctx.Next()
}

// requireAuth middleware checks if user is logged in
func requireAuth(ctx rweb.Context) error {
	if !ctx.Has("user_id") {
		// set flash message about needing to login
		setFlashMessage(ctx, "error", "Please login to access this page")
		return ctx.Redirect(302, "/")
	}
	return ctx.Next()
}

// homePage component for the main page content
type homePage struct {
	Flash  map[string]string
	UserID string
}

func (h homePage) Render(b *element.Builder) any {
	b.H1().T("RWeb Cookie Example")

	// display flash message if present
	if h.Flash != nil {
		element.RenderComponents(b, flashMessage{
			Type:    h.Flash["type"],
			Message: h.Flash["message"],
		})
	}

	// show login form or user info based on auth status
	if h.UserID != "" {
		// logged in user section
		b.P().R(
			b.T("Welcome, User ", h.UserID, "! "),
			b.A("href", "/dashboard").T("Go to Dashboard"),
		)
		b.Form("method", "POST", "action", "/logout").R(
			b.Button("type", "submit").T("Logout"),
		)
	} else {
		// login form
		b.P().T("Please login to continue.")
		b.Form("method", "POST", "action", "/login").R(
			b.Label().R(
				b.T("Username: "),
				b.Input("type", "text", "name", "username", "required", "required"),
			),
			b.Br(),
			b.Label().R(
				b.T("Password: "),
				b.Input("type", "password", "name", "password", "required", "required"),
			),
			b.Br(),
			b.Label().R(
				b.Input("type", "checkbox", "name", "remember", "value", "1"),
				b.T(" Remember me"),
			),
			b.Br(),
			b.Button("type", "submit").T("Login"),
		)
		b.P().T("Hint: Use any username and password \"secret\"")
	}

	// flash message demo section
	b.Hr()
	b.H3().T("Flash Message Demo")
	b.Form("method", "POST", "action", "/flash-demo").R(
		b.Label().R(
			b.T("Message: "),
			b.Input("type", "text", "name", "message", "required", "required"),
		),
		b.Button("type", "submit").T("Set Flash Message"),
	)

	return nil
}

// homeHandler displays the home page with any flash messages
func homeHandler(ctx rweb.Context) error {
	b := element.NewBuilder()

	// get user ID if logged in
	userID := ""
	if ctx.Has("user_id") {
		userID = ctx.Get("user_id").(string)
	}

	// get flash message
	flash := getFlashMessage(ctx)

	// render page using components
	page := pageLayout{
		Title: "RWeb Cookie Example",
		Body: homePage{
			Flash:  flash,
			UserID: userID,
		},
	}

	element.RenderComponents(b, page)
	return ctx.WriteHTML(b.String())
}

// loginHandler handles login form submission
func loginHandler(ctx rweb.Context) error {
	username := ctx.Request().GetPostValue("username")
	password := ctx.Request().GetPostValue("password")
	remember := ctx.Request().GetPostValue("remember")

	// simple auth check (use proper auth in production)
	if password != "secret" {
		setFlashMessage(ctx, "error", "Invalid credentials")
		return ctx.Redirect(302, "/")
	}

	// create session
	sessionID := generateSessionID()
	sessions[sessionID] = map[string]interface{}{
		"user_id":  username,
		"login_at": time.Now(),
	}

	// set session cookie
	if remember == "1" {
		// persistent cookie for "remember me"
		cookie := &rweb.Cookie{
			Name:    "session_id",
			Value:   sessionID,
			Expires: time.Now().Add(30 * 24 * time.Hour), // 30 days
			MaxAge:  30 * 24 * 60 * 60,                   // 30 days in seconds
		}
		ctx.SetCookieWithOptions(cookie)
	} else {
		// session cookie (expires on browser close)
		ctx.SetCookie("session_id", sessionID)
	}

	setFlashMessage(ctx, "success", fmt.Sprintf("Welcome back, %s!", username))
	return ctx.Redirect(302, "/dashboard")
}

// logoutHandler clears the session
func logoutHandler(ctx rweb.Context) error {
	if ctx.HasCookie("session_id") {
		sessionID, _ := ctx.GetCookie("session_id")
		delete(sessions, sessionID)
		ctx.DeleteCookie("session_id")
	}

	setFlashMessage(ctx, "success", "You have been logged out")
	return ctx.Redirect(302, "/")
}

// dashboardPage component for the dashboard content
type dashboardPage struct {
	UserID        string
	LoginTime     time.Time
	SessionLength time.Duration
}

func (d dashboardPage) Render(b *element.Builder) any {
	b.H1().T("Dashboard")
	b.DivClass("info").R(
		b.P().R(
			b.Strong().T("User ID: "),
			b.T(d.UserID),
		),
		b.P().R(
			b.Strong().T("Logged in at: "),
			b.T(d.LoginTime.Format("15:04:05")),
		),
		b.P().R(
			b.Strong().T("Session duration: "),
			b.T(d.SessionLength.Round(time.Second).String()),
		),
	)
	b.P().R(
		b.A("href", "/").T("Back to Home"),
	)
	return nil
}

// dashboardHandler shows the protected dashboard
func dashboardHandler(ctx rweb.Context) error {
	userID := ctx.Get("user_id").(string)
	session := ctx.Get("session").(map[string]interface{})
	loginTime := session["login_at"].(time.Time)

	b := element.NewBuilder()

	// render page using components
	page := pageLayout{
		Title: "Dashboard - RWeb Cookie Example",
		Body: dashboardPage{
			UserID:        userID,
			LoginTime:     loginTime,
			SessionLength: time.Since(loginTime),
		},
	}

	element.RenderComponents(b, page)
	return ctx.WriteHTML(b.String())
}

// flashDemoHandler demonstrates flash messages
func flashDemoHandler(ctx rweb.Context) error {
	message := ctx.Request().GetPostValue("message")
	setFlashMessage(ctx, "success", message)
	return ctx.Redirect(302, "/")
}

// setFlashMessage creates a flash message cookie
func setFlashMessage(ctx rweb.Context, msgType, message string) {
	flash := map[string]string{
		"type":    msgType,
		"message": message,
	}

	// encode as JSON and base64
	jsonData, _ := json.Marshal(flash)
	encoded := base64.StdEncoding.EncodeToString(jsonData)

	// flash cookies are short-lived
	cookie := &rweb.Cookie{
		Name:   "flash",
		Value:  encoded,
		MaxAge: 60, // 60 seconds
	}
	ctx.SetCookieWithOptions(cookie)
}

// getFlashMessage retrieves and clears a flash message
func getFlashMessage(ctx rweb.Context) map[string]string {
	encoded, err := ctx.GetCookieAndClear("flash")
	if err != nil {
		return nil
	}

	// decode from base64 and JSON
	jsonData, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil
	}

	var flash map[string]string
	if err := json.Unmarshal(jsonData, &flash); err != nil {
		return nil
	}

	return flash
}

// generateSessionID creates a simple session ID
func generateSessionID() string {
	return fmt.Sprintf("sess_%d", time.Now().UnixNano())
}
