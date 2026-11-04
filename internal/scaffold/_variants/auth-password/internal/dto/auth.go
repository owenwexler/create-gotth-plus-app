package dto

// Email fields are trimmed and lowercased by the handlers before validation. Password lengths
// are checked by auth.CheckPasswordLength; max here only caps what is parsed.

// LoginRequest is the sign-in form.
type LoginRequest struct {
	Email    string `validate:"required,email,max=320"`
	Password string `validate:"required,max=1024"`
	Next     string `validate:"max=2048"`
}

// SignupRequest is the sign-up form.
type SignupRequest struct {
	Email    string `validate:"required,email,max=320"`
	Password string `validate:"required,max=1024"`
}

// EmailRequest is a form with only an email (forgot password).
type EmailRequest struct {
	Email string `validate:"required,email,max=320"`
}

// TokenRequest is a form that carries an emailed token (confirm email).
type TokenRequest struct {
	Token string `validate:"required,max=128"`
}

// ResetPasswordRequest is the new-password form.
type ResetPasswordRequest struct {
	Token    string `validate:"required,max=128"`
	Password string `validate:"required,max=1024"`
}
