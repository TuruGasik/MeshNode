package bmkg

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"
)

type State struct {
	LastSentID string `json:"last_sent_id"`
	UpdatedAt  string `json:"updated_at"`
}

func LoadState(path string) (State, error) {
	if strings.TrimSpace(path) == "" {
		return State{}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, err
	}
	defer file.Close()

	var state State
	if err := json.NewDecoder(file).Decode(&state); err != nil {
		return State{}, err
	}
	return state, nil
}

func SaveState(path, lastSentID string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	state := State{
		LastSentID: lastSentID,
		UpdatedAt:  time.Now().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
