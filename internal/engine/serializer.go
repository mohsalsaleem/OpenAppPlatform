package engine

import (
	"context"
	"sync"
)

// Serializer runs operations one at a time per application, in the order they
// were submitted, while different applications run in parallel. This keeps
// two plans from interleaving on the same application and matches SQLite's
// single writer.
type Serializer struct {
	run func(context.Context, Operation) error

	mu    sync.Mutex
	lanes map[string]*lane
	wg    sync.WaitGroup
}

type lane struct {
	queue   []pending
	running bool
}

type pending struct {
	ctx  context.Context
	op   Operation
	done chan error
}

// NewSerializer returns a serializer that runs operations with run.
func NewSerializer(run func(context.Context, Operation) error) *Serializer {
	return &Serializer{run: run, lanes: map[string]*lane{}}
}

// Submit queues an operation behind earlier ones for the same application.
// The returned channel receives the operation's result.
func (s *Serializer) Submit(ctx context.Context, op Operation) <-chan error {
	done := make(chan error, 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.lanes[op.Application]
	if l == nil {
		l = &lane{}
		s.lanes[op.Application] = l
	}
	l.queue = append(l.queue, pending{ctx: ctx, op: op, done: done})
	if !l.running {
		l.running = true
		s.wg.Add(1)
		go s.drain(op.Application, l)
	}
	return done
}

func (s *Serializer) drain(app string, l *lane) {
	defer s.wg.Done()
	for {
		s.mu.Lock()
		if len(l.queue) == 0 {
			l.running = false
			delete(s.lanes, app)
			s.mu.Unlock()
			return
		}
		p := l.queue[0]
		l.queue = l.queue[1:]
		s.mu.Unlock()
		if err := p.ctx.Err(); err != nil {
			p.done <- err
			continue
		}
		p.done <- s.run(p.ctx, p.op)
	}
}

// Wait blocks until every submitted operation has finished.
func (s *Serializer) Wait() { s.wg.Wait() }
