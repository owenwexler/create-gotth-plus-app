package dto

// HelloRequest is the validated body of the example app's hello form.
type HelloRequest struct {
	Name string `validate:"required,min=1,max=50"`
}
