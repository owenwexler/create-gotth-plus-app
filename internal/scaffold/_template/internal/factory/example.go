package factory

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"

	"github.com/owenwexler/gotth-plus/internal/dto"
	"github.com/owenwexler/gotth-plus/internal/service"
	"github.com/owenwexler/gotth-plus/internal/view"
)

// This file is part of the example app. See "Removing the example app" in README.md.

// helloValidationMessage turns a validator error for the hello form into something we can show the user.
func helloValidationMessage(err error) string {
	var validationErrors validator.ValidationErrors

	if !errors.As(err, &validationErrors) || len(validationErrors) == 0 {
		return "Please check your name and try again"
	}

	fieldError := validationErrors[0]

	if fieldError.Field() == "Name" {
		switch fieldError.Tag() {
		case "required", "min":
			return "Please enter your name"
		case "max":
			return "Your name must be " + fieldError.Param() + " characters or fewer"
		}
	}

	return "Please check your name and try again"
}

// Hello answers the hello form with the greeting fragment that replaces #greeting.
func Hello(svc service.GreetingService, validate *validator.Validate) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		errorToast := view.ErrorToast("Error saying hello")

		// Parse form data sent by htmx
		if err := r.ParseForm(); err != nil {
			logRejected(r, "parse hello form", err)
			respondWithToast(w, r, http.StatusBadRequest, errorToast)
			return
		}

		// Extract specific fields using the input 'name' attributes
		req := dto.HelloRequest{Name: strings.TrimSpace(r.FormValue("name"))}

		if err := validate.Struct(&req); err != nil {
			logRejected(r, "hello validation failed", err)
			respondWithToast(w, r, http.StatusUnprocessableEntity, view.ToastArgs{Kind: view.ToastError, Header: "Error saying hello", Body: helloValidationMessage(err)})
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		if err := view.Greeting(svc.Greet(req.Name)).Render(r.Context(), w); err != nil {
			logError(r, "render greeting", err)
		}
	}
}
