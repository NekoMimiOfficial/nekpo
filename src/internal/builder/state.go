package builder

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type RepoState struct {
	Built    string `json:"built"`
	Failed   string `json:"failed"`
	FailedAt int64  `json:"failed_at"`
}

type State struct {
	path  string
	Repos map[string]*RepoState `json:"repos"`
}

func LoadState(workDir string) *State {
	s := &State{path: filepath.Join(workDir, "state.json"), Repos: map[string]*RepoState{}}
	if raw, err := os.ReadFile(s.path); err == nil {
		json.Unmarshal(raw, s)
	}
	if s.Repos == nil {
		s.Repos = map[string]*RepoState{}
	}
	return s
}

func (s *State) Get(name string) *RepoState {
	if s.Repos[name] == nil {
		s.Repos[name] = &RepoState{}
	}
	return s.Repos[name]
}

func (s *State) Save() error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(s.path+".tmp", raw, 0o644); err != nil {
		return err
	}
	return os.Rename(s.path+".tmp", s.path)
}
