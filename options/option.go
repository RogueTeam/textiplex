package options

type Option[T any] struct {
	Error   error
	Success T
}
