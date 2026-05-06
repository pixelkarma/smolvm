package main

import (
	"fmt"
	"sync"
)

type instanceJobStore struct {
	mu   sync.RWMutex
	jobs map[int64]instanceJob
}

type instanceJob struct {
	Status string
}

func newInstanceJobStore() *instanceJobStore {
	return &instanceJobStore{jobs: make(map[int64]instanceJob)}
}

func (s *instanceJobStore) begin(id int64, status string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job, ok := s.jobs[id]; ok && isPendingStatus(job.Status) {
		return false
	}
	s.jobs[id] = instanceJob{Status: status}
	return true
}

func (s *instanceJobStore) fail(id int64, action string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[id] = instanceJob{Status: fmt.Sprintf("%s-failed", action)}
	_ = err
}

func (s *instanceJobStore) clear(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.jobs, id)
}

func (s *instanceJobStore) status(id int64) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	if !ok {
		return "", false
	}
	return job.Status, true
}

func isPendingStatus(status string) bool {
	switch status {
	case "starting", "stopping", "deleting":
		return true
	default:
		return false
	}
}
