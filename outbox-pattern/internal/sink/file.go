package sink

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
)

type FileSink struct {
	Path       string
	FailMode   string
	DedupePath string

	mu sync.Mutex
}

type EventEnvelope struct {
	EventID   string          `json:"event_id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	Attempt   int             `json:"attempt"`
}

type Sink interface {
	Emit(context.Context, EventEnvelope) error
}

func NewFileSink(path, failMode, dedupePath string) (*FileSink, error) {
	if path == "" {
		return nil, errors.New("sink path is required")
	}
	if dedupePath != "" && !dedupeLockSupported() {
		return nil, errors.New("dedupe sink mode requires Unix file locking")
	}
	if failMode != "" && failMode != "before-write" && failMode != "after-write" {
		return nil, errors.New("unsupported sink failure mode")
	}
	return &FileSink{Path: path, FailMode: failMode, DedupePath: dedupePath}, nil
}

func (s *FileSink) Emit(_ context.Context, envelope EventEnvelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.FailMode == "before-write" {
		return errors.New("injected sink failure before write")
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')

	file, err := os.OpenFile(s.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if s.FailMode == "after-write" {
		if err := file.Close(); err != nil {
			return err
		}
		return errors.New("injected sink failure after write")
	}
	if err := file.Close(); err != nil {
		return err
	}
	if s.DedupePath != "" {
		return s.applyDeduplicated(envelope)
	}
	return nil
}

func (s *FileSink) applyDeduplicated(envelope EventEnvelope) error {
	return withDedupeLock(s.DedupePath, func() error {
		return s.applyDeduplicatedLocked(envelope)
	})
}

func (s *FileSink) applyDeduplicatedLocked(envelope EventEnvelope) error {
	file, err := os.Open(s.DedupePath)
	if err == nil {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			var previous struct {
				EventID string `json:"event_id"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &previous); err != nil {
				_ = file.Close()
				return err
			}
			if previous.EventID == envelope.EventID {
				if err := file.Close(); err != nil {
					return err
				}
				return nil
			}
		}
		if err := scanner.Err(); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	payload, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	file, err = os.OpenFile(s.DedupePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(file, string(payload)); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
