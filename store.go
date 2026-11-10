package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	mu    sync.RWMutex
	path  string
	state State
}

func openStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "manager.json"), state: State{Version: 1, People: []Person{}, Credentials: []Credential{}, Audit: []Audit{}}}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, &s.state); err != nil {
		return nil, err
	}
	if s.state.Version != 1 {
		return nil, errors.New("不支持的数据文件版本")
	}
	if s.state.People == nil {
		s.state.People = []Person{}
	}
	if s.state.Credentials == nil {
		s.state.Credentials = []Credential{}
	}
	if s.state.Audit == nil {
		s.state.Audit = []Audit{}
	}
	return s, nil
}

func cloneState(s State) State {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	var result State
	if err = json.Unmarshal(b, &result); err != nil {
		panic(err)
	}
	return result
}

func (s *Store) Snapshot() State { s.mu.RLock(); defer s.mu.RUnlock(); return cloneState(s.state) }

func (s *Store) Update(fn func(*State) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneState(s.state)
	if err := fn(&next); err != nil {
		return err
	}
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".manager-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// Rename commits the whole snapshot; a crash never exposes half-written JSON.
	if err = os.Rename(name, s.path); err != nil {
		return err
	}
	s.state = next
	if directory, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}
