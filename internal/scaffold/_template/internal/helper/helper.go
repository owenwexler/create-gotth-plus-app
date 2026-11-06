package helper

// Define a reusable generic ternary helper
func Ternary[T any](cond bool, vTrue, vFalse T) T {
	if cond {
		return vTrue
	}
	return vFalse
}

// Reusable count function
func Count[T any](slice []T, test func(T) bool) int {
	count := 0
	for _, item := range slice {
		if test(item) {
			count++
		}
	}
	return count
}
