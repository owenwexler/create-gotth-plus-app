package service

import "strings"

// GreetingService defines business logic for the example app's greeting.
type GreetingService interface {
	Greet(name string) string
}

type greetingService struct{}

// NewGreetingService creates a new GreetingService.
func NewGreetingService() GreetingService {
	return &greetingService{}
}

// Greet returns "Hello <name>!", or "Hello!" when there is nobody to greet yet.
func (s *greetingService) Greet(name string) string {
	name = strings.TrimSpace(name)

	if name == "" {
		return "Hello!"
	}

	return "Hello " + name + "!"
}
