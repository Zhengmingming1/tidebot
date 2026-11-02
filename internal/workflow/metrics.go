package workflow

// Metrics counts process-local activity without unbounded run or node labels.
type Metrics struct {
	Active  int    `json:"active"`
	Retries uint64 `json:"retries"`
}

func (s *Service) Metrics() Metrics {
	s.mu.Lock()
	active := len(s.active)
	s.mu.Unlock()
	return Metrics{Active: active, Retries: s.retries.Load()}
}
