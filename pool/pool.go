package pool

// Reduces memory fragmentation by creating data in larger pages
type Pool[T any] struct {
	pageSize int
	data     []T
}

func (p *Pool[T]) Get() (v *T) {
	if len(p.data) == 0 {
		p.data = make([]T, p.pageSize)
	}
	v = &p.data[0]
	p.data = p.data[1:]
	return v
}

func New[T any](pageSize int) (p *Pool[T]) {
	return &Pool[T]{
		pageSize: pageSize,
		data:     make([]T, pageSize),
	}
}
