package testdata

import (
	"bufio"
	"os"
	"strings"
)

// Route represents a single line in the router test file.
type Route struct {
	Method string
	Path   string
}

// Routes loads all routes from a text file.
func Routes(fileName string) []Route {
	var routes []Route

	for line := range Lines(fileName) {
		line = strings.TrimSpace(line)
		parts := strings.Split(line, " ")
		routes = append(routes, Route{
			Method: parts[0],
			Path:   parts[1],
		})
	}

	return routes
}

// Lines is a utility function to easily read every line in a text file.
func Lines(fileName string) <-chan string {
	lines := make(chan string)

	go func() {
		defer close(lines)
		file, err := os.Open(fileName)

		if err != nil {
			return
		}

		defer file.Close()
		scanner := bufio.NewScanner(file)

		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	return lines
}
